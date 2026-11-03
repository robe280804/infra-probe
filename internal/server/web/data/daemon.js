// Configuration facts derived from the Docker daemon data (info + daemon.json).

const LOOPBACK_CIDRS = new Set(["127.0.0.0/8", "::1/128"]);
const isInternalRegistry = (name) => /^(localhost|127\.|\[::1\])/.test(name) || name.includes("docker.internal");

/** Daemon settings that deserve attention, as {level, text}. */
export function daemonFindings(daemon) {
  const out = [];
  const info = daemon?.info;
  if (!info) return out;

  const cfg = daemon.config?.content ?? {};
  const opts = info.security_options ?? [];

  const hosts = Array.isArray(cfg.hosts) ? cfg.hosts : [];
  const tcp = hosts.filter((h) => String(h).startsWith("tcp://"));
  if (tcp.length && !cfg.tlsverify) out.push({ level: "bad", text: `API listens on TCP without TLS verification (${tcp.join(", ")})` });

  for (const w of info.warnings ?? []) out.push({ level: "warn", text: `Daemon warning: ${w}` });

  if (!info.live_restore) out.push({ level: "warn", text: "Live restore is off: containers stop when the daemon restarts" });
  if (info.logging_driver === "json-file" && !cfg["log-opts"]?.["max-size"]) {
    out.push({ level: "warn", text: "json-file logs have no rotation (no max-size in daemon.json): they grow without limit" });
  }

  const insecure = [
    ...(info.registry?.insecure_cidrs ?? []).filter((c) => !LOOPBACK_CIDRS.has(c)),
    ...(info.registry?.indexes ?? []).filter((i) => !i.secure && !i.official && !isInternalRegistry(i.name)).map((i) => i.name),
  ];
  if (insecure.length) out.push({ level: "warn", text: `Insecure registries allowed: ${insecure.join(", ")}` });

  if (!opts.some((o) => o.startsWith("name=seccomp"))) out.push({ level: "warn", text: "No default seccomp profile" });
  if (info.debug) out.push({ level: "warn", text: "Debug mode is on" });
  if (info.experimental) out.push({ level: "warn", text: "Experimental features are enabled" });

  if (info.rootless) out.push({ level: "ok", text: "The daemon runs rootless" });
  if (opts.some((o) => o.startsWith("name=userns"))) out.push({ level: "ok", text: "User namespace remapping is enabled" });
  if (info.swarm?.state === "active") out.push({ level: "ok", text: `Swarm node ${info.swarm.control_available ? "(manager)" : "(worker)"} is active` });

  return out;
}
