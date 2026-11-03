// Configuration facts derived from a container's inspect data.
// Field names follow the Docker inspect JSON (e.g. HostConfig.Privileged).

export const isRoot = (user) => !user || user === "root" || user === "0" || user.startsWith("0:") || user.startsWith("root:");
export const isAllInterfaces = (ip) => !ip || ip === "0.0.0.0" || ip === "::";
export const isDockerSock = (path) => (path || "").endsWith("docker.sock");

/** Published ports as rows: {container, host, exposedOnly, public}. */
export function portRows(inspect) {
  const ports = inspect?.NetworkSettings?.Ports ?? {};
  const rows = [];
  for (const [port, bindings] of Object.entries(ports)) {
    if (!bindings || bindings.length === 0) {
      rows.push({ container: port, host: "not published", exposedOnly: true, public: false });
      continue;
    }
    for (const b of bindings) {
      rows.push({ container: port, host: `${b.HostIp || "0.0.0.0"}:${b.HostPort}`, exposedOnly: false, public: isAllInterfaces(b.HostIp) });
    }
  }
  return rows;
}

/**
 * Quick configuration findings for one container. This is a first, informal
 * pass shown in the UI; a dedicated rules engine will replace it later.
 */
export function findings(c) {
  const out = [];
  const inspect = c.inspect;
  if (!inspect) return out;

  const hc = inspect.HostConfig ?? {};
  const cfg = inspect.Config ?? {};
  const running = c.state === "running";

  if (hc.Privileged) out.push({ level: "bad", text: "Runs in privileged mode" });
  for (const m of inspect.Mounts ?? []) {
    if (isDockerSock(m.Source) || isDockerSock(m.Destination)) {
      out.push({ level: "bad", text: `Docker socket mounted at ${m.Destination}` });
    }
  }
  for (const opt of hc.SecurityOpt ?? []) {
    if (/unconfined/.test(opt)) out.push({ level: "bad", text: `Security profile disabled (${opt})` });
  }
  if (inspect.State?.OOMKilled) out.push({ level: "bad", text: "Was killed by the out-of-memory killer" });
  if (inspect.State?.Health?.Status === "unhealthy") out.push({ level: "bad", text: "Health check is failing" });

  if (isRoot(cfg.User)) out.push({ level: "warn", text: "Runs as root" });
  if (hc.NetworkMode === "host") out.push({ level: "warn", text: "Uses the host network" });
  if (hc.PidMode === "host") out.push({ level: "warn", text: "Shares the host PID namespace" });
  if (hc.IpcMode === "host") out.push({ level: "warn", text: "Shares the host IPC namespace" });
  if ((hc.CapAdd ?? []).length) out.push({ level: "warn", text: `Extra capabilities: ${hc.CapAdd.join(", ")}` });
  const exposed = portRows(inspect).filter((p) => p.public);
  if (exposed.length) out.push({ level: "warn", text: `Published on all interfaces: ${exposed.map((p) => p.container).join(", ")}` });
  if (running && !hc.Memory) out.push({ level: "warn", text: "No memory limit set" });

  return out;
}

export const worst = (list) => (list.some((f) => f.level === "bad") ? "bad" : list.length ? "warn" : "ok");
