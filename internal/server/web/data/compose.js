// Facts derived from a (sanitized) compose file. Environment values are
// already masked by the collector: only names reach this code.

import { isAllInterfaces, isDockerSock } from "./findings.js";

const asList = (v) => (Array.isArray(v) ? v : v === undefined || v === null ? [] : [v]);

/** Published ports of a service as {text, public}. Handles the short and long syntax. */
export function composePorts(svc) {
  const out = [];
  for (const p of asList(svc.ports)) {
    if (typeof p === "object" && p !== null) {
      if (p.published === undefined) continue; // exposed only
      out.push({ text: `${p.host_ip ?? "0.0.0.0"}:${p.published}->${p.target}`, public: isAllInterfaces(p.host_ip) });
      continue;
    }
    const text = String(p);
    const parts = text.split(":");
    if (parts.length < 2) continue; // "80": container port only, not published
    // "ip:host:container" (an IPv6 address would add colons: rare, shown as is)
    const hostIp = parts.length >= 3 ? parts.slice(0, -2).join(":").replace(/^\[|\]$/g, "") : "";
    out.push({ text, public: isAllInterfaces(hostIp) });
  }
  return out;
}

/** Names of the environment variables of a service (values are masked). */
export function composeEnvNames(svc) {
  const env = svc.environment;
  if (!env) return [];
  if (Array.isArray(env)) return env.map((e) => String(e).split("=")[0]);
  return Object.keys(env);
}

/** Mounts of a service as {source, target, readOnly}. */
export function composeMounts(svc) {
  return asList(svc.volumes).map((v) => {
    if (typeof v === "object" && v !== null) return { source: v.source ?? "", target: v.target ?? "", readOnly: !!v.read_only };
    const [source, target, mode] = String(v).split(":");
    return target === undefined ? { source: "", target: source, readOnly: false } : { source, target, readOnly: mode === "ro" };
  });
}

/** Configuration facts worth attention, as {level, text}. */
export function composeFlags(svc) {
  const out = [];
  if (svc.privileged === true) out.push({ level: "bad", text: "privileged" });
  if (composeMounts(svc).some((m) => isDockerSock(m.source))) out.push({ level: "bad", text: "Docker socket mounted" });
  if (svc.network_mode === "host") out.push({ level: "warn", text: "host network" });
  if (svc.pid === "host") out.push({ level: "warn", text: "host PID namespace" });
  const caps = asList(svc.cap_add);
  if (caps.length) out.push({ level: "warn", text: `cap_add ${caps.join(", ")}` });
  const exposed = composePorts(svc).filter((p) => p.public);
  if (exposed.length) out.push({ level: "warn", text: `${exposed.length} port${exposed.length > 1 ? "s" : ""} on all interfaces` });
  return out;
}
