// Container detail page: resources, security, limits, network, mounts, process.

import { h } from "../lib/dom.js";
import { fmtBytes, fmtPct, fmtDate, fmtNanos } from "../lib/format.js";
import { badge, stateBadge, card, tile, kv, table, chips, rawJson } from "../lib/ui.js";
import { meter, lineChart } from "../lib/charts.js";
import { containersOf, imagesOf, networksOf, volumesOf, imageHref, networkHref, volumeHref } from "../data/report.js";
import { findings, portRows, isDockerSock } from "../data/findings.js";
import { link, yesNo, notFound } from "./shared.js";

function resourcesCard(c, history) {
  const hc = c.inspect?.HostConfig ?? {};
  const body = [];

  if (c.state !== "running") {
    body.push(h("div", { class: "muted", text: "The container is not running: no live statistics." }));
    return card("Resources", ...body);
  }
  if (!c.stats) {
    body.push(h("div", { class: "muted", text: c.stats_error ? `Statistics unavailable: ${c.stats_error}` : "Statistics unavailable." }));
    return card("Resources", ...body);
  }

  const s = c.stats;

  // Memory: a meter only makes sense against a configured limit.
  if (s.memory_limit > 0) {
    body.push(meter({ label: "Memory", value: s.memory_usage, max: s.memory_limit, format: fmtBytes }));
  } else {
    body.push(h("div", { class: "meter" }, h("div", { class: "meter-head" }, h("span", { class: "meter-label", text: "Memory" }), h("span", { class: "meter-value", text: fmtBytes(s.memory_usage) })), badge("No memory limit set: the container can use all host memory", "warn")));
  }

  // CPU: limit in cores comes from NanoCpus or from quota/period.
  const cores = hc.NanoCpus > 0 ? hc.NanoCpus / 1e9 : hc.CpuQuota > 0 && hc.CpuPeriod > 0 ? hc.CpuQuota / hc.CpuPeriod : 0;
  if (cores > 0) {
    body.push(meter({ label: `CPU (limit ${cores} cores)`, value: s.cpu_percent, max: cores * 100, format: (v) => fmtPct(v, 0) }));
  } else {
    body.push(h("div", { class: "meter" }, h("div", { class: "meter-head" }, h("span", { class: "meter-label", text: "CPU" }), h("span", { class: "meter-value", text: fmtPct(s.cpu_percent) })), h("div", { class: "muted", text: "No CPU limit set (100% = one core)" })));
  }

  if (hc.PidsLimit > 0) {
    body.push(meter({ label: "Processes", value: s.pids, max: hc.PidsLimit, format: (v) => String(v) }));
  }

  body.push(
    h(
      "div",
      { class: "tiles" },
      tile("Processes", String(s.pids), hc.PidsLimit > 0 ? null : "no limit"),
      tile("Network", `↓ ${fmtBytes(s.net_rx_bytes)}`, `↑ ${fmtBytes(s.net_tx_bytes)}`),
      tile("Disk I/O", `R ${fmtBytes(s.block_read_bytes)}`, `W ${fmtBytes(s.block_write_bytes)}`),
    ),
  );

  const samples = history ?? [];
  if (samples.length > 0) {
    body.push(
      lineChart({
        title: "Memory",
        points: samples.map((p) => ({ t: p.t, v: p.mem })),
        format: fmtBytes,
        limit: s.memory_limit > 0 ? s.memory_limit : 0,
        minMax: 1024 * 1024,
      }),
      lineChart({
        title: "CPU",
        points: samples.map((p) => ({ t: p.t, v: p.cpu })),
        format: (v) => fmtPct(v, 0),
        limit: cores > 0 ? cores * 100 : 0,
        minMax: 10,
      }),
    );
  }
  return card("Resources", ...body);
}

export function containerDetail(report, id, history) {
  const c = containersOf(report).find((x) => x.id === id);
  if (!c) return notFound("container", "#/containers");

  const inspect = c.inspect;
  const hc = inspect?.HostConfig ?? {};
  const cfg = inspect?.Config ?? {};
  const state = inspect?.State ?? {};
  const list = findings(c);
  const image = imagesOf(report).find((i) => i.id === c.image_id);

  const head = h(
    "div",
    {},
    link("#/containers", "← Containers"),
    h("div", { class: "page-head" }, h("h1", { text: c.name }), stateBadge(c.state), state.Health ? badge(`health: ${state.Health.Status}`, state.Health.Status === "healthy" ? "ok" : state.Health.Status === "unhealthy" ? "bad" : "warn") : null),
    h("p", { class: "subtitle" }, "Image ", image ? link(imageHref(image), c.image) : c.image, " · ", h("span", { class: "mono", text: c.id })),
  );

  if (!inspect) {
    return h("div", {}, head, h("div", { class: "banner", text: `Configuration unavailable: ${c.inspect_error ?? "unknown error"}` }));
  }

  const attention = list.length
    ? card("Attention", h("ul", { class: "findings" }, list.map((f) => h("li", {}, badge(f.level === "bad" ? "Risk" : "Warning", f.level), f.text))))
    : card("Attention", badge("No findings", "ok"));

  const general = card("General", kv([
    ["ID", h("span", { class: "mono", text: inspect.Id })],
    ["Status", c.status],
    ["Created", fmtDate(inspect.Created)],
    ["Started", fmtDate(state.StartedAt)],
    ["Finished", fmtDate(state.FinishedAt)],
    ["Exit code", state.Running ? "—" : String(state.ExitCode ?? "—")],
    ["Restart count", String(inspect.RestartCount ?? 0)],
    ["Platform", inspect.Platform],
    ["Driver", inspect.Driver],
    ["PID (host)", state.Pid ? String(state.Pid) : "—"],
    ["OOM killed", yesNo(state.OOMKilled, true)],
    ["Error", state.Error],
  ]));

  const security = card("Security", kv([
    ["Privileged", yesNo(hc.Privileged, true)],
    ["User", cfg.User ? cfg.User : h("span", { class: "caution", text: "root (default)" })],
    ["Read-only root filesystem", yesNo(hc.ReadonlyRootfs)],
    ["Capabilities added", chips(hc.CapAdd)],
    ["Capabilities dropped", chips(hc.CapDrop)],
    ["Security options", chips(hc.SecurityOpt)],
    ["Network mode", hc.NetworkMode],
    ["PID mode", hc.PidMode || "private"],
    ["IPC mode", hc.IpcMode],
    ["User namespace", hc.UsernsMode || "—"],
    ["Devices", chips((hc.Devices ?? []).map((d) => `${d.PathOnHost} → ${d.PathInContainer}`))],
  ]));

  const cores = hc.NanoCpus > 0 ? `${hc.NanoCpus / 1e9} cores` : hc.CpuQuota > 0 && hc.CpuPeriod > 0 ? `${hc.CpuQuota / hc.CpuPeriod} cores` : "unlimited";
  const limits = card("Limits & restart", kv([
    ["Memory limit", hc.Memory > 0 ? fmtBytes(hc.Memory) : h("span", { class: "caution", text: "unlimited" })],
    ["Memory reservation", hc.MemoryReservation > 0 ? fmtBytes(hc.MemoryReservation) : "—"],
    ["Swap limit", hc.MemorySwap > 0 ? fmtBytes(hc.MemorySwap) : hc.MemorySwap === -1 ? "unlimited" : "—"],
    ["CPU limit", cores],
    ["CPU shares", hc.CpuShares ? String(hc.CpuShares) : "—"],
    ["CPU set", hc.CpusetCpus],
    ["Processes limit", hc.PidsLimit > 0 ? String(hc.PidsLimit) : "unlimited"],
    ["Restart policy", hc.RestartPolicy?.Name ? `${hc.RestartPolicy.Name}${hc.RestartPolicy.MaximumRetryCount ? ` (max ${hc.RestartPolicy.MaximumRetryCount})` : ""}` : "no"],
    ["Auto remove", yesNo(hc.AutoRemove)],
    ["Log driver", hc.LogConfig?.Type],
  ]));

  const networks = Object.entries(inspect.NetworkSettings?.Networks ?? {}).map(([name, n]) => ({ name, ...n }));
  const ports = portRows(inspect);
  const network = card(
    "Network",
    table(
      [
        {
          title: "Network",
          render: (n) => {
            const known = networksOf(report).find((x) => x.name === n.name);
            return known ? link(networkHref(known), n.name) : n.name;
          },
        },
        { title: "IP address", render: (n) => n.IPAddress || "—" },
        { title: "Gateway", render: (n) => n.Gateway || "—" },
        { title: "MAC", render: (n) => n.MacAddress || "—" },
      ],
      networks,
      { empty: "Not attached to any network." },
    ),
    h("h2", { text: "Ports" }),
    table(
      [
        { title: "Container port", render: (p) => p.container },
        { title: "Published on", render: (p) => p.host },
        { title: "Exposure", render: (p) => (p.exposedOnly ? badge("internal only", "ok") : p.public ? badge("all interfaces", "warn") : badge("restricted", "ok")) },
      ],
      ports,
      { empty: "No ports." },
    ),
  );

  const mounts = card(
    "Mounts",
    table(
      [
        { title: "Type", render: (m) => m.Type },
        {
          title: "Source",
          render: (m) => {
            // Named volumes link to their page; bind mounts show the host path.
            const vol = m.Type === "volume" && m.Name ? volumesOf(report).find((v) => v.name === m.Name) : null;
            return vol ? link(volumeHref(vol), vol.anonymous ? `volume ${m.Name.slice(0, 12)}` : m.Name) : h("span", { class: "mono" }, m.Source || m.Name || "—");
          },
        },
        { title: "Destination", render: (m) => h("span", { class: "mono", text: m.Destination }) },
        { title: "Access", render: (m) => (m.RW ? badge("read-write", isDockerSock(m.Source) ? "bad" : "neutral") : badge("read-only", "ok")) },
      ],
      inspect.Mounts ?? [],
      { empty: "No mounts." },
    ),
  );

  const hcheck = cfg.Healthcheck;
  const process = card("Process & environment", kv([
    ["Entrypoint", cfg.Entrypoint?.length ? h("code", { text: cfg.Entrypoint.join(" ") }) : "—"],
    ["Command", cfg.Cmd?.length ? h("code", { text: cfg.Cmd.join(" ") }) : "—"],
    ["Resolved", h("code", { text: [inspect.Path, ...(inspect.Args ?? [])].join(" ") })],
    ["Working dir", cfg.WorkingDir || "/"],
    ["Hostname", cfg.Hostname],
    ["Environment (names only)", chips((cfg.Env ?? []).map((e) => e.split("=")[0]))],
    ["Healthcheck", hcheck?.Test?.length ? h("code", { text: hcheck.Test.join(" ") }) : "none"],
    ["Health interval", hcheck ? fmtNanos(hcheck.Interval) : "—"],
  ]));

  const labels = Object.entries(cfg.Labels ?? {});
  const labelsCard = card(
    "Labels",
    table([{ title: "Key", render: ([k]) => h("span", { class: "mono", text: k }) }, { title: "Value", render: ([, v]) => v }], labels, { empty: "No labels." }),
  );

  return h(
    "div",
    {},
    head,
    h("div", { class: "grid" }, attention, resourcesCard(c, history)),
    h("div", { class: "grid" }, general, security, limits),
    h("div", { class: "grid" }, network, mounts),
    h("div", { class: "grid" }, process, labelsCard),
    rawJson("Raw inspect JSON", inspect),
  );
}
