// Security rules page: what the network can reach, the ufw configuration and
// the kernel firewall rules.

import { h } from "../lib/dom.js";
import { badge, card, tile, kv, table, rawJson } from "../lib/ui.js";
import { containersOf, containerHref } from "../data/report.js";
import { firewallOf, firewallContext, exposures, firewallFindings, iptablesChain, filteringRules } from "../data/firewall.js";
import { link, page } from "./shared.js";

const mono = (text) => h("span", { class: "mono", text });

function findingsCard(items) {
  return card(
    "Attention",
    items.length === 0
      ? badge("No findings", "ok")
      : h("ul", { class: "findings" }, items.map((f) => h("li", {}, badge(f.level === "bad" ? "Risk" : "Warning", f.level), f.text))),
  );
}

function ufwState(ufw) {
  if (!ufw) return badge("not installed");
  if (!ufw.enabled) return badge("disabled", "warn");
  if (ufw.loaded === false) return badge("enabled, not loaded", "warn");
  return badge("enabled", "ok");
}

function setupCard(ctx) {
  const { fw, ufw } = ctx;
  const dockerUser = iptablesChain(fw, "filter", "DOCKER-USER");
  const kernel = [];
  if (fw.iptables) kernel.push(`iptables (${fw.iptables.backend || "unknown backend"})`);
  if (fw.ip6tables) kernel.push("ip6tables");
  if (fw.nftables) kernel.push("nftables");

  return card(
    "Firewall",
    kv([
      ["ufw", ufwState(ufw)],
      ["Incoming by default", ufw?.default_input],
      ["Outgoing by default", ufw?.default_output],
      ["Routed by default", ufw?.default_forward],
      ["Kernel rules", ctx.canRead ? kernel.join(", ") || "no tool installed" : badge("not readable without CAP_NET_ADMIN")],
      ["DOCKER-USER", dockerUser ? `${filteringRules(dockerUser).length} filtering rules` : ctx.canRead ? "no chain" : null],
      ["Docker port filtering", ctx.dockerNAT ? `Docker NAT rules (${ctx.dockerBackend || "iptables"}): ufw does not apply` : "host rules (no Docker NAT)"],
    ]),
  );
}

function exposuresCard(rows, hasHost) {
  return card(
    "Reachable from outside",
    table(
      [
        { title: "Port", num: true, render: (r) => r.port },
        { title: "Protocol", render: (r) => r.proto },
        { title: "Address", render: (r) => mono(r.address) },
        {
          title: "Service",
          render: (r) =>
            r.docker
              ? h("div", { class: "stack-list" }, r.containers.map((c) => link(containerHref(c), c.name)))
              : r.process
                ? `${r.process} (${r.pid})`
                : h("span", { class: "muted", text: "unknown process" }),
        },
        { title: "Firewall", render: (r) => badge(r.verdict.text, r.verdict.level) },
      ],
      rows,
      { empty: "Nothing listens on a non-loopback address." },
    ),
    h("p", {
      class: "muted",
      text: "Ports bound to a non-loopback address. Docker-published ports go through Docker's NAT rules: ufw does not filter them, only rules in the DOCKER-USER chain do (or bind the port to 127.0.0.1).",
    }),
    hasHost ? null : h("p", { class: "muted", text: "The host listening ports were not collected: only the Docker-published ports are listed." }),
  );
}

function ufwRulesCard(ufw) {
  if (!ufw) return null;
  if (!ufw.rules_readable) {
    return card("ufw rules", h("p", { class: "muted", text: "The rules are readable by root only (/etc/ufw/user.rules): run the probe as root to see them." }));
  }
  return card(
    "ufw rules",
    table(
      [
        { title: "Action", render: (r) => badge(`${r.route ? "route " : ""}${r.action}`, r.action === "allow" ? "ok" : r.action === "limit" ? "neutral" : "bad") },
        { title: "Direction", render: (r) => (r.interface ? `${r.direction} on ${r.interface}` : r.direction) },
        { title: "Port", render: (r) => h("span", {}, mono(r.port), r.app ? ` (${r.app})` : null) },
        { title: "Protocol", render: (r) => r.protocol },
        { title: "From", render: (r) => mono(r.from) },
        { title: "To", render: (r) => mono(r.to) },
        { title: "IPv6", render: (r) => (r.ipv6 ? "yes" : "") },
        { title: "Comment", render: (r) => r.comment || "" },
      ],
      ufw.rules,
      { empty: "No rule: only the default policies apply." },
    ),
  );
}

/** One collapsible block per table, the rules of each chain in order. */
function rulesBlock(title, tables, chainTitle) {
  return tables.map((t) => {
    const count = t.chains.reduce((n, c) => n + c.rules.length, 0);
    return h(
      "details",
      { class: "raw card" },
      h("summary", { text: `${title} · ${t.family ? `${t.family} ` : ""}${t.name} (${t.chains.length} chains, ${count} rules)` }),
      t.chains.map((c) => h("div", {}, h("h3", { text: chainTitle(c) }), h("pre", { text: c.rules.length ? c.rules.join("\n") : "(no rule)" }))),
    );
  });
}

const iptablesTitle = (c) => (c.policy ? `${c.name} · policy ${c.policy}` : c.name);
const nftTitle = (c) => [c.name, c.hook ? `hook ${c.hook}` : null, c.policy ? `policy ${c.policy}` : null].filter(Boolean).join(" · ");

export function firewallPage(report) {
  const fw = firewallOf(report);
  if (!fw) return page("Security rules", null, h("div", { class: "banner", text: "No firewall data was collected." }));

  const ctx = firewallContext(report);
  const rows = exposures(report, containersOf(report));
  const findings = firewallFindings(report, rows);
  const notes = (fw.notes ?? []).map((n) => h("div", { class: "banner", text: n }));
  const bypass = rows.filter((r) => r.docker && r.verdict.level === "bad").length;
  const open = rows.filter((r) => r.verdict.level === "warn" || r.verdict.level === "bad").length;

  return page(
    "Security rules",
    "What the network can reach on this machine, and what filters it.",
    ...notes,
    h(
      "div",
      { class: "tiles" },
      tile("ufw", ctx.ufw ? (ctx.ufwActive ? "active" : "inactive") : "absent", ctx.ufw?.default_input ? `incoming: ${ctx.ufw.default_input}` : null),
      tile("Kernel rules", ctx.canRead ? "readable" : "hidden", ctx.canRead ? null : "needs CAP_NET_ADMIN"),
      tile("Reachable ports", String(rows.length), "non-loopback"),
      tile("Not filtered", String(open), "open or bypassing ufw"),
      tile("Bypass ufw", String(bypass), "Docker-published ports"),
    ),
    h("div", { class: "grid" }, findingsCard(findings), setupCard(ctx)),
    exposuresCard(rows, !!report?.results?.host?.listeners),
    ufwRulesCard(ctx.ufw),
    ...(fw.iptables ? rulesBlock("iptables", fw.iptables.tables, iptablesTitle) : []),
    ...(fw.ip6tables ? rulesBlock("ip6tables", fw.ip6tables.tables, iptablesTitle) : []),
    ...(fw.nftables ? rulesBlock("nftables", fw.nftables.tables, nftTitle) : []),
    rawJson("Raw firewall JSON", fw),
  );
}
