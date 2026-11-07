// Facts derived from the firewall data, the host listeners and the published
// container ports: what the network can reach and what filters it.
//
// The same analysis runs in the zip export (export/summary_firewall.go): keep
// the two in step.

import { daemonOf } from "./report.js";

/** The firewall section of the report, or null when it was not collected. */
export const firewallOf = (report) => report?.results?.firewall ?? null;

const isLoopback = (addr) => addr.startsWith("127.") || addr === "::1" || addr === "localhost";
const isAnywhere = (addr) => !addr || addr === "any" || addr === "0.0.0.0/0" || addr === "::/0";
const protoMatches = (rule, proto) => !rule || rule === "any" || rule === proto;

/** Checks a port against a ufw port list: "any", "22", "80,443", "6000:6007". */
export function portMatches(rule, port) {
  if (!rule || rule === "any") return true;
  const p = Number(port);
  return rule.split(",").some((item) => {
    const [lo, hi = lo] = item.split(":").map(Number);
    return p >= lo && p <= hi;
  });
}

/** A chain of the IPv4 iptables rules, or null. */
export function iptablesChain(fw, table, chain) {
  const t = (fw?.iptables?.tables ?? []).find((x) => x.name === table);
  return t?.chains.find((c) => c.name === chain) ?? null;
}

/** The rules of a chain other than the "-j RETURN" Docker puts in DOCKER-USER. */
export const filteringRules = (chain) => (chain?.rules ?? []).filter((r) => r.trim() !== "-j RETURN");

/** The facts the verdicts depend on. */
export function firewallContext(report) {
  const fw = firewallOf(report) ?? {};
  const ufw = fw.ufw ?? null;
  const daemon = daemonOf(report);
  return {
    fw,
    ufw,
    // ufw.conf says enabled and the kernel does not say otherwise.
    ufwActive: !!ufw && ufw.enabled && ufw.loaded !== false,
    canRead: !!fw.can_read_rules,
    // Docker publishes through its own NAT rules (FORWARD path), which ufw
    // never sees. Not with iptables disabled or a rootless daemon.
    dockerNAT: !(daemon?.info?.rootless || daemon?.config?.content?.iptables === false),
    dockerBackend: daemon?.info?.firewall_backend ?? "",
  };
}

/** Whether the kernel rules filter incoming traffic. */
export function inputFiltered(ctx) {
  const input = iptablesChain(ctx.fw, "filter", "INPUT");
  if (input && (input.policy === "DROP" || input.policy === "REJECT" || input.rules.length > 0)) return true;
  return (ctx.fw.nftables?.tables ?? []).some((t) => t.chains.some((c) => c.hook === "input" && (c.policy === "drop" || c.rules.length > 0)));
}

function nftForwardFiltered(ctx) {
  return (ctx.fw.nftables?.tables ?? [])
    .filter((t) => !t.name.startsWith("docker"))
    .some((t) => t.chains.some((c) => c.hook === "forward" && (c.policy === "drop" || c.rules.length > 0)));
}

/** What the firewall does with a connection to a host port (INPUT path). */
export function hostVerdict(ctx, proto, port) {
  const { ufw } = ctx;
  if (ctx.ufwActive) {
    const denyByDefault = ["DROP", "REJECT"].includes((ufw.default_input ?? "").toUpperCase());
    if (!ufw.rules_readable) {
      return denyByDefault
        ? { level: "neutral", text: "ufw denies by default; its rules are readable by root only" }
        : { level: "warn", text: "ufw allows by default" };
    }
    const rule = ufw.rules.find((r) => !r.route && r.direction === "in" && protoMatches(r.protocol, proto) && portMatches(r.port, port));
    if (rule) {
      const on = rule.interface ? ` on ${rule.interface}` : "";
      if (rule.action === "allow" || rule.action === "limit") {
        const what = rule.action === "limit" ? "rate limited" : "allowed";
        return isAnywhere(rule.from)
          ? { level: "warn", text: `ufw: ${what} from anywhere${on}` }
          : { level: "ok", text: `ufw: ${what} only from ${rule.from}${on}` };
      }
      return { level: "ok", text: `ufw: blocked by a ${rule.action} rule${on}` };
    }
    return denyByDefault ? { level: "ok", text: "ufw: blocked (no rule, default deny)" } : { level: "warn", text: "ufw: allowed (no rule, default allow)" };
  }
  if (!ctx.canRead) return { level: "neutral", text: "unknown: the firewall rules are not readable" };
  if (inputFiltered(ctx)) return { level: "neutral", text: "filtered by iptables/nftables rules: review them" };
  return { level: "warn", text: "open: no firewall rule filters incoming traffic" };
}

/** What the firewall does with a connection to a port published by Docker. */
export function dockerVerdict(ctx, proto, port) {
  if (!ctx.dockerNAT) {
    const v = hostVerdict(ctx, proto, port);
    return { ...v, text: `${v.text} (Docker without NAT rules: host rules apply)` };
  }
  const open = ctx.ufwActive ? "bad" : "warn";
  const bypass = ctx.ufwActive ? "; ufw does not apply to Docker ports" : "";
  if (!ctx.canRead) return { level: open, text: `open unless DOCKER-USER rules filter it (not readable)${bypass}` };
  if (ctx.dockerBackend === "nftables") {
    return nftForwardFiltered(ctx)
      ? { level: "neutral", text: "filtered by your nftables forward rules: review them" }
      : { level: open, text: `open: no nftables forward rule of yours filters it${bypass}` };
  }
  if (filteringRules(iptablesChain(ctx.fw, "filter", "DOCKER-USER")).length > 0) {
    return { level: "neutral", text: "DOCKER-USER has rules: review them" };
  }
  return { level: open, text: `open: no DOCKER-USER rule${bypass}` };
}

/**
 * The ports reachable from the network, Docker ports first:
 * [{proto, port, address, docker, containers, process, verdict}].
 */
export function exposures(report, containers) {
  const ctx = firewallContext(report);
  const docker = new Map();
  const dockerPorts = new Set();
  for (const c of containers) {
    for (const [key, bindings] of Object.entries(c.inspect?.NetworkSettings?.Ports ?? {})) {
      const proto = key.split("/")[1] ?? "tcp";
      for (const b of bindings ?? []) {
        const address = b.HostIp || "0.0.0.0";
        if (isLoopback(address)) continue;
        const k = `${proto}|${b.HostPort}|${address}`;
        if (!docker.has(k)) docker.set(k, { proto, port: b.HostPort, address, docker: true, containers: [] });
        const row = docker.get(k);
        if (!row.containers.includes(c)) row.containers.push(c);
        dockerPorts.add(`${proto}:${b.HostPort}`);
      }
    }
  }

  const rows = [...docker.values()].map((r) => ({ ...r, verdict: dockerVerdict(ctx, r.proto, r.port) }));
  const seen = new Set();
  for (const l of report?.results?.host?.listeners ?? []) {
    if (l.loopback) continue;
    const proto = l.protocol.replace("6", "");
    const port = String(l.port);
    if (dockerPorts.has(`${proto}:${port}`)) continue; // the proxy of a published port
    const k = `${proto}|${port}|${l.address}`;
    if (seen.has(k)) continue;
    seen.add(k);
    rows.push({ proto, port, address: l.address, docker: false, process: l.process, pid: l.pid, verdict: hostVerdict(ctx, proto, port) });
  }

  // Plain string order, as in the Go summary.
  const cmp = (x, y) => (x < y ? -1 : x > y ? 1 : 0);
  return rows.sort((a, b) => b.docker - a.docker || Number(a.port) - Number(b.port) || cmp(a.proto, b.proto) || cmp(a.address, b.address));
}

/** Firewall findings worth attention, as {level, text}, worst first. */
export function firewallFindings(report, rows) {
  const ctx = firewallContext(report);
  const out = [];
  const { ufw } = ctx;

  const bypass = [...new Set(rows.filter((r) => r.docker && r.verdict.level === "bad").map((r) => `${r.proto}/${r.port}`))];
  if (bypass.length) {
    out.push({ level: "bad", text: `Docker publishes ${bypass.join(", ")} on the network and ufw does not filter them: add DOCKER-USER rules or bind them to 127.0.0.1.` });
  }
  if (ufw) {
    if (!ufw.enabled) out.push({ level: "warn", text: "ufw is installed but disabled." });
    else if (ufw.loaded === false) out.push({ level: "warn", text: "ufw is enabled in ufw.conf but its rules are not loaded in the kernel." });
    else if ((ufw.default_input ?? "").toUpperCase() === "ACCEPT") out.push({ level: "warn", text: "ufw accepts incoming connections by default." });
  }
  if (!ctx.ufwActive && ctx.canRead && !inputFiltered(ctx)) out.push({ level: "warn", text: "No firewall rule filters incoming traffic." });

  const open = [...new Set(rows.filter((r) => r.verdict.level === "warn").map((r) => `${r.proto}/${r.port}`))];
  if (open.length) out.push({ level: "warn", text: `Reachable from any network: ${open.join(", ")}.` });
  return out;
}
