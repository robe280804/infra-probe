// Docker daemon page: version, drivers, security, registries, disk usage, daemon.json.

import { h } from "../lib/dom.js";
import { fmtBytes } from "../lib/format.js";
import { badge, card, tile, kv, table, chips, rawJson } from "../lib/ui.js";
import { daemonOf } from "../data/report.js";
import { daemonFindings } from "../data/daemon.js";
import { page, yesNo } from "./shared.js";

const mono = (text) => h("span", { class: "mono", text });
const onOff = (value) => badge(value ? "on" : "off", value ? "ok" : "neutral");

function findingsCard(daemon) {
  const items = daemonFindings(daemon);
  return card(
    "Configuration notes",
    items.length === 0
      ? h("div", { class: "empty", text: "Nothing to report." })
      : h("div", { class: "badges stack" }, items.map((f) => badge(f.text, f.level))),
  );
}

function diskCard(du) {
  if (!du) return card("Disk usage", h("div", { class: "muted", text: "Unavailable: the disk usage call failed." }));
  const rows = [
    ["Images", du.images],
    ["Containers", du.containers],
    ["Volumes", du.volumes],
    ["Build cache", du.build_cache],
  ];
  return card(
    "Disk usage",
    table(
      [
        { title: "Kind", render: ([name]) => name },
        { title: "Total", num: true, render: ([, t]) => String(t.total) },
        { title: "Active", num: true, render: ([, t]) => String(t.active) },
        { title: "Size", num: true, render: ([, t]) => fmtBytes(t.size) },
        { title: "Reclaimable", num: true, render: ([, t]) => fmtBytes(t.reclaimable) },
      ],
      rows,
    ),
    h("p", { class: "muted", text: `${fmtBytes(du.total_size)} in use, ${fmtBytes(du.reclaimable)} reclaimable by pruning unused objects.` }),
  );
}

export function daemonPage(report) {
  const daemon = daemonOf(report);
  const info = daemon?.info;
  const version = daemon?.version;

  if (!info && !version) {
    return page("Docker daemon", null, h("div", { class: "banner", text: "The daemon could not be queried." }));
  }

  const cfg = daemon.config;
  const subtitle = [version?.version && `Engine ${version.version}`, info?.operating_system, info?.name].filter(Boolean).join(" · ");

  const tiles = h(
    "div",
    { class: "tiles" },
    tile("Engine", version?.version ?? info?.server_version ?? "—", version ? `API ${version.api_version}` : null),
    tile("Storage driver", info?.storage_driver ?? "—"),
    tile("Logging driver", info?.logging_driver ?? "—"),
    tile("Cgroup", info ? `v${info.cgroup_version || "?"}` : "—", info?.cgroup_driver),
    tile("Default runtime", info?.default_runtime ?? "—"),
    tile("Live restore", info ? (info.live_restore ? "On" : "Off") : "—"),
  );

  const general = info
    ? card("Engine and host", kv([
        ["Version", version?.version ?? info.server_version],
        ["API version", version ? `${version.api_version} (min ${version.min_api_version || "—"})` : null],
        ["Platform", version?.platform],
        ["Host name", info.name],
        ["Operating system", [info.operating_system, info.os_version].filter(Boolean).join(" ")],
        ["Kernel", info.kernel_version],
        ["Architecture", info.architecture],
        ["CPUs", String(info.ncpu)],
        ["Memory", fmtBytes(info.mem_total)],
        ["Docker root dir", mono(info.docker_root_dir)],
        ["Containers", `${info.containers} (${info.containers_running} running, ${info.containers_paused} paused, ${info.containers_stopped} stopped)`],
        ["Images", String(info.images)],
        ["Swarm", info.swarm.state + (info.swarm.state === "active" ? (info.swarm.control_available ? " (manager)" : " (worker)") : "")],
      ]))
    : null;

  const drivers = info
    ? card("Drivers and runtime", kv([
        ["Storage driver", info.storage_driver],
        ["Storage details", info.storage_driver_status?.length ? chips(info.storage_driver_status.map((p) => `${p.key}: ${p.value}`)) : null],
        ["Logging driver", info.logging_driver],
        ["Cgroup driver", `${info.cgroup_driver} (v${info.cgroup_version || "?"})`],
        ["Isolation", info.isolation],
        ["Default runtime", info.default_runtime],
        ["Runtimes", chips(info.runtimes)],
        ["Firewall backend", info.firewall_backend],
        ["Default address pools", chips((info.default_address_pools ?? []).map((p) => `${p.base} /${p.size}`))],
        ["Plugins (volume)", chips(info.plugins.volume)],
        ["Plugins (network)", chips(info.plugins.network)],
        ["Plugins (log)", chips(info.plugins.log)],
        ["Plugins (authorization)", chips(info.plugins.authorization)],
      ]))
    : null;

  const security = info
    ? card("Security", kv([
        ["Rootless", yesNo(info.rootless)],
        ["Live restore", onOff(info.live_restore)],
        ["Debug", yesNo(info.debug, true)],
        ["Experimental", yesNo(info.experimental, true)],
        ["Security options", chips(info.security_options)],
        ["IPv4 forwarding", yesNo(info.ipv4_forwarding)],
        ["Memory limit support", yesNo(info.memory_limit)],
        ["Swap limit support", yesNo(info.swap_limit)],
        ["PIDs limit support", yesNo(info.pids_limit)],
      ]))
    : null;

  const registries = info
    ? card(
        "Registries and proxy",
        table(
          [
            { title: "Registry", render: (i) => mono(i.name) },
            { title: "TLS", render: (i) => (i.secure ? badge("secure", "ok") : badge("insecure", "warn")) },
            { title: "Official", render: (i) => (i.official ? "Yes" : "—") },
            { title: "Mirrors", render: (i) => chips(i.mirrors) },
          ],
          info.registry.indexes,
          { empty: "No registry configured." },
        ),
        kv([
          ["Insecure networks", chips(info.registry.insecure_cidrs)],
          ["Mirrors", chips(info.registry.mirrors)],
          ["HTTP proxy", info.proxy.http],
          ["HTTPS proxy", info.proxy.https],
          ["No proxy", info.proxy.no_proxy],
        ]),
      )
    : null;

  const components = version
    ? card(
        "Components",
        table(
          [
            { title: "Name", render: (c) => c.name },
            { title: "Version", render: (c) => c.version },
            { title: "Details", render: (c) => chips(Object.entries(c.details ?? {}).map(([k, v]) => `${k}: ${v}`)) },
          ],
          version.components,
          { empty: "No component reported." },
        ),
      )
    : null;

  const labels = info?.labels?.length ? card("Daemon labels", chips(info.labels)) : null;

  const config = cfg
    ? rawJson(`daemon.json (${cfg.path})`, cfg.content)
    : card("daemon.json", h("div", { class: "muted", text: "No daemon.json found: the daemon runs with its defaults, or it is not on this machine." }));

  return page(
    "Docker daemon",
    subtitle,
    tiles,
    h("div", { class: "grid" }, findingsCard(daemon), diskCard(daemon.disk_usage)),
    h("div", { class: "grid" }, general, drivers),
    h("div", { class: "grid" }, security, registries),
    h("div", { class: "grid" }, components, labels),
    config,
    info ? rawJson("Raw daemon info JSON", info) : null,
  );
}
