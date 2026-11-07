// Facts derived from the host data and from the containers it runs.

/** The host section of the report, or an empty object. */
export const hostOf = (report) => report?.results?.host ?? {};

/**
 * Published container ports indexed by "protocol:port" (tcp:8080), so that a
 * socket the host listens on can be traced to the container behind it.
 * Docker publishes through a proxy process or NAT rules: either way the host
 * port is what shows up in the container's port bindings.
 */
export function publishedPorts(containers) {
  const map = new Map();
  for (const c of containers) {
    for (const [key, bindings] of Object.entries(c.inspect?.NetworkSettings?.Ports ?? {})) {
      const proto = key.split("/")[1] ?? "tcp";
      for (const b of bindings ?? []) {
        const k = `${proto}:${b.HostPort}`;
        if (!map.has(k)) map.set(k, []);
        map.get(k).push({ container: c, containerPort: key.split("/")[0], hostIp: b.HostIp });
      }
    }
  }
  return map;
}

/** The containers behind a listener ("tcp6" counts as tcp). */
export function containersFor(listener, published) {
  const proto = listener.protocol.replace("6", "");
  return (published.get(`${proto}:${listener.port}`) ?? []).map((p) => p.container);
}

/** How a listener can be reached: a badge level and text. */
export function exposure(l) {
  if (l.all_interfaces) return { level: "warn", text: "all interfaces" };
  if (l.loopback) return { level: "ok", text: "local only" };
  return { level: "neutral", text: l.address };
}

/** Load average compared with the number of CPUs: > 1 per CPU means queueing. */
export function loadLevel(load, cpus) {
  if (!cpus) return "neutral";
  const perCpu = load / cpus;
  if (perCpu >= 1.5) return "bad";
  if (perCpu >= 1) return "warn";
  return "ok";
}

/** Some file systems (9p, FUSE) report more free inodes than inodes: ignore those numbers. */
export const inodesValid = (f) => f.inodes_total > 0 && f.inodes_free <= f.inodes_total;

/** Host findings worth attention, as {level, text}. */
export function hostFindings(host) {
  const out = [];
  const sys = host.system;
  if (sys && host.num_cpu) {
    const level = loadLevel(sys.load_5, host.num_cpu);
    if (level !== "ok" && level !== "neutral") out.push({ level, text: `High load: ${sys.load_5.toFixed(2)} (5 min) on ${host.num_cpu} CPUs` });
  }
  const mem = host.memory;
  if (mem && mem.total > 0 && mem.available / mem.total < 0.1) out.push({ level: "bad", text: "Less than 10% of the memory is available" });
  if (mem && mem.swap_total > 0 && mem.swap_free / mem.swap_total < 0.2) out.push({ level: "warn", text: "Swap is almost full" });

  for (const f of host.filesystems ?? []) {
    if (f.read_only || f.total === 0) continue;
    if (f.used_percent >= 90) out.push({ level: "bad", text: `${f.mountpoint} is ${f.used_percent.toFixed(0)}% full` });
    else if (f.used_percent >= 80) out.push({ level: "warn", text: `${f.mountpoint} is ${f.used_percent.toFixed(0)}% full` });
    if (inodesValid(f) && f.inodes_free / f.inodes_total < 0.1) out.push({ level: "warn", text: `${f.mountpoint} is running out of inodes` });
  }

  const failed = (host.systemd?.services ?? []).filter((u) => u.active === "failed");
  if (failed.length) out.push({ level: "bad", text: `Failed services: ${failed.map((u) => u.name).join(", ")}` });
  return out;
}
