// Page renderers. Each takes the current report (and history) and returns a Node.
// Field names follow the Docker inspect JSON (e.g. HostConfig.Privileged).

import {
  h, badge, stateBadge, card, scrollCard, tile, kv, table, chips, rawJson, meter, miniMeter, rankedBars, lineChart,
  filterBar, countedOptions, countBy,
  fmtBytes, fmtPct, fmtDate, fmtNanos, fmtList, shortId,
} from "./components.js";

// ---------- data helpers ----------

export const dockerOf = (report) => report?.results?.docker ?? { containers: [], images: [] };
export const containersOf = (report) => dockerOf(report).containers ?? [];
export const imagesOf = (report) => dockerOf(report).images ?? [];

const link = (hash, text) => h("a", { href: hash, text });
const containerHref = (c) => `#/containers/${encodeURIComponent(c.id)}`;
const imageHref = (i) => `#/images/${encodeURIComponent(i.id)}`;

const isRoot = (user) => !user || user === "root" || user === "0" || user.startsWith("0:") || user.startsWith("root:");
const isAllInterfaces = (ip) => !ip || ip === "0.0.0.0" || ip === "::";
const isDockerSock = (path) => (path || "").endsWith("docker.sock");

/** Published ports as rows: {container, host, exposedOnly, public}. */
function portRows(inspect) {
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

const worst = (list) => (list.some((f) => f.level === "bad") ? "bad" : list.length ? "warn" : "ok");

function page(title, subtitle, ...children) {
  return h(
    "div",
    {},
    h("div", { class: "page-head" }, h("h1", { text: title })),
    subtitle ? h("p", { class: "subtitle" }, subtitle) : null,
    ...children,
  );
}

const yesNo = (value, riskyWhenTrue = false) =>
  h("span", { class: value && riskyWhenTrue ? "risk" : "", text: value ? "Yes" : "No" });

// ---------- overview ----------

export function overview(report) {
  const containers = containersOf(report);
  const images = imagesOf(report);
  const running = containers.filter((c) => c.state === "running");
  const withFindings = containers.map((c) => ({ c, f: findings(c) })).filter((x) => x.f.length > 0);
  withFindings.sort((a, b) => b.f.filter((x) => x.level === "bad").length - a.f.filter((x) => x.level === "bad").length);

  const host = report?.results?.host ?? {};
  const topMemory = running
    .filter((c) => c.stats)
    .sort((a, b) => b.stats.memory_usage - a.stats.memory_usage)
    .slice(0, 8);

  return page(
    "Overview",
    `Host ${host.hostname ?? "—"}`,
    h(
      "div",
      { class: "tiles" },
      tile("Containers", String(containers.length), `${running.length} running`),
      tile("Stopped", String(containers.length - running.length)),
      tile("Images", String(images.length)),
      tile("Unused images", String(images.filter((i) => i.containers === 0).length), "not used by any container"),
      tile("With findings", String(withFindings.length), "containers to review"),
    ),
    h(
      "div",
      { class: "grid" },
      scrollCard(
        "Containers needing attention",
        withFindings.length === 0
          ? h("div", { class: "empty", text: "No findings." })
          : table(
              [
                { title: "Container", render: ({ c }) => link(containerHref(c), c.name) },
                { title: "Findings", render: ({ f }) => h("div", { class: "badges" }, f.slice(0, 3).map((x) => badge(x.text, x.level)), f.length > 3 ? badge(`+${f.length - 3} more`) : null) },
              ],
              withFindings,
              { href: ({ c }) => containerHref(c) },
            ),
      ),
      scrollCard(
        "Memory in use (top containers)",
        topMemory.length === 0
          ? h("div", { class: "empty", text: "No running containers with stats." })
          : rankedBars(topMemory.map((c) => ({ label: c.name, value: c.stats.memory_usage, href: containerHref(c) })), fmtBytes),
      ),
      scrollCard("Host", kv(Object.entries(host).map(([k, v]) => [k, String(v)]))),
    ),
  );
}

// ---------- containers ----------

export const defaultContainerFilters = () => ({
  q: "",
  state: "all",
  image: "all",
  health: "all",
  findings: "all",
  memLimit: "all",
  exposure: "all",
  sort: "name",
});

const CONTAINER_SORTS = {
  name: (a, b) => a.c.name.localeCompare(b.c.name),
  memory: (a, b) => (b.c.stats?.memory_usage ?? -1) - (a.c.stats?.memory_usage ?? -1),
  memoryPct: (a, b) => (b.c.stats?.memory_percent ?? -1) - (a.c.stats?.memory_percent ?? -1),
  cpu: (a, b) => (b.c.stats?.cpu_percent ?? -1) - (a.c.stats?.cpu_percent ?? -1),
  findings: (a, b) => b.f.length - a.f.length,
  created: (a, b) => Date.parse(b.c.created_at) - Date.parse(a.c.created_at),
};

function healthBadge(health) {
  if (health === "healthy") return badge("healthy", "ok");
  if (health === "unhealthy") return badge("unhealthy", "bad");
  if (health === "starting") return badge("starting", "warn");
  return h("span", { class: "muted", text: "—" });
}

/** True when the container row passes every active filter. */
function containerMatches(row, f) {
  const { c } = row;
  const q = f.q.trim().toLowerCase();
  if (q && ![c.name, c.image, c.id].some((v) => String(v).toLowerCase().includes(q))) return false;
  if (f.state !== "all" && c.state !== f.state) return false;
  if (f.image !== "all" && c.image !== f.image) return false;
  if (f.health !== "all" && row.health !== f.health) return false;

  const hasBad = row.f.some((x) => x.level === "bad");
  if (f.findings === "any" && row.f.length === 0) return false;
  if (f.findings === "risk" && !hasBad) return false;
  if (f.findings === "warn" && (row.f.length === 0 || hasBad)) return false;
  if (f.findings === "none" && row.f.length > 0) return false;

  const limited = (c.inspect?.HostConfig?.Memory ?? 0) > 0;
  if (f.memLimit === "limited" && !limited) return false;
  if (f.memLimit === "unlimited" && limited) return false;

  const published = row.ports.some((p) => !p.exposedOnly);
  const publicPort = row.ports.some((p) => p.public);
  if (f.exposure === "public" && !publicPort) return false;
  if (f.exposure === "published" && !published) return false;
  if (f.exposure === "none" && published) return false;

  return true;
}

export function containerList(report, filters) {
  const defaults = defaultContainerFilters();
  const rows = containersOf(report).map((c) => ({
    c,
    f: findings(c),
    ports: portRows(c.inspect),
    health: c.inspect?.State?.Health?.Status ?? "none",
  }));

  const columns = [
    { title: "Name", render: ({ c }) => link(containerHref(c), c.name) },
    { title: "State", render: ({ c }) => stateBadge(c.state) },
    { title: "Health", render: ({ health }) => healthBadge(health) },
    { title: "Image", render: ({ c }) => c.image },
    {
      title: "Memory",
      render: ({ c }) => {
        if (!c.stats) return h("span", { class: "muted", text: "—" });
        if (c.stats.memory_limit > 0) {
          return miniMeter(c.stats.memory_percent, `${fmtBytes(c.stats.memory_usage)} / ${fmtBytes(c.stats.memory_limit)}`);
        }
        return h("span", {}, fmtBytes(c.stats.memory_usage), " ", badge("no limit", "warn"));
      },
    },
    { title: "CPU", num: true, render: ({ c }) => (c.stats ? fmtPct(c.stats.cpu_percent) : "—") },
    {
      title: "Findings",
      render: ({ f }) => (f.length ? badge(`${f.length}`, worst(f)) : badge("none", "ok")),
    },
    { title: "Status", render: ({ c }) => c.status },
  ];

  const results = h("div");
  const update = () => {
    const shown = rows.filter((r) => containerMatches(r, filters)).sort(CONTAINER_SORTS[filters.sort] ?? CONTAINER_SORTS.name);
    results.replaceChildren(
      h("p", { class: "subtitle", text: `${shown.length} of ${rows.length} containers shown` }),
      card(null, table(columns, shown, { href: ({ c }) => containerHref(c), empty: "No containers match the filters." })),
    );
  };

  const bar = filterBar(
    [
      { key: "q", type: "search", label: "Search", placeholder: "Name, image or ID…" },
      { key: "state", type: "select", label: "State", options: countedOptions("All states", countBy(rows, (r) => r.c.state)) },
      { key: "image", type: "select", label: "Image", options: countedOptions("All images", countBy(rows, (r) => r.c.image)) },
      {
        key: "health", type: "select", label: "Health",
        options: [
          { value: "all", label: "Any" },
          { value: "healthy", label: "Healthy" },
          { value: "unhealthy", label: "Unhealthy" },
          { value: "starting", label: "Starting" },
          { value: "none", label: "No health check" },
        ],
      },
      {
        key: "findings", type: "select", label: "Findings",
        options: [
          { value: "all", label: "Any" },
          { value: "any", label: "With findings" },
          { value: "risk", label: "With risks" },
          { value: "warn", label: "Warnings only" },
          { value: "none", label: "Clean" },
        ],
      },
      {
        key: "memLimit", type: "select", label: "Memory limit",
        options: [
          { value: "all", label: "Any" },
          { value: "limited", label: "With limit" },
          { value: "unlimited", label: "Without limit" },
        ],
      },
      {
        key: "exposure", type: "select", label: "Ports",
        options: [
          { value: "all", label: "Any" },
          { value: "public", label: "Open on all interfaces" },
          { value: "published", label: "Published (any)" },
          { value: "none", label: "Not published" },
        ],
      },
      {
        key: "sort", type: "select", label: "Sort by",
        options: [
          { value: "name", label: "Name" },
          { value: "memory", label: "Memory used" },
          { value: "memoryPct", label: "Memory % of limit" },
          { value: "cpu", label: "CPU" },
          { value: "findings", label: "Most findings" },
          { value: "created", label: "Newest first" },
        ],
      },
    ],
    filters,
    defaults,
    update,
  );

  update();
  return page("Containers", null, bar, results);
}

// ---------- container detail ----------

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
        { title: "Network", render: (n) => n.name },
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
        { title: "Source", render: (m) => h("span", { class: "mono" }, m.Source || m.Name || "—") },
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

// ---------- images ----------

export const defaultImageFilters = () => ({
  q: "",
  usage: "all",
  tag: "all",
  size: "all",
  platform: "all",
  user: "all",
  sort: "size",
});

const MB = 1024 * 1024;

const IMAGE_SORTS = {
  size: (a, b) => b.i.size - a.i.size,
  name: (a, b) => a.name.localeCompare(b.name),
  created: (a, b) => Date.parse(b.i.created_at) - Date.parse(a.i.created_at),
  usedBy: (a, b) => b.i.containers - a.i.containers,
};

function imageMatches(row, f) {
  const { i } = row;
  const q = f.q.trim().toLowerCase();
  if (q && ![...(i.repo_tags ?? []), i.id].some((v) => String(v).toLowerCase().includes(q))) return false;
  if (f.usage === "used" && i.containers === 0) return false;
  if (f.usage === "unused" && i.containers > 0) return false;
  if (f.tag === "tagged" && i.dangling) return false;
  if (f.tag === "untagged" && !i.dangling) return false;
  if (f.size !== "all" && i.size < Number(f.size) * MB) return false;
  if (f.platform !== "all" && row.platform !== f.platform) return false;
  if (f.user === "root" && !row.root) return false;
  if (f.user === "nonroot" && row.root) return false;
  return true;
}

export function imageList(report, filters) {
  const defaults = defaultImageFilters();
  const rows = imagesOf(report).map((i) => ({
    i,
    name: (i.repo_tags ?? [])[0] ?? "~",
    platform: i.inspect ? `${i.inspect.Os ?? "?"}/${i.inspect.Architecture ?? "?"}` : "unknown",
    root: i.inspect ? isRoot(i.inspect.Config?.User) : false,
  }));

  const columns = [
    { title: "Tags", render: ({ i }) => link(imageHref(i), (i.repo_tags ?? []).join(", ") || "<none>") },
    { title: "Size", num: true, render: ({ i }) => fmtBytes(i.size) },
    { title: "Used by", num: true, render: ({ i }) => String(i.containers) },
    { title: "Platform", render: ({ platform }) => platform },
    { title: "User", render: ({ i, root }) => (!i.inspect ? "—" : root ? badge("root", "warn") : badge("non-root", "ok")) },
    {
      title: "Flags",
      render: ({ i }) => h("div", { class: "badges" }, i.dangling ? badge("untagged", "warn") : null, i.containers === 0 ? badge("unused", "warn") : null),
    },
    { title: "ID", render: ({ i }) => h("span", { class: "mono", text: shortId(i.id) }) },
    { title: "Created", render: ({ i }) => fmtDate(i.created_at) },
  ];

  const results = h("div");
  const update = () => {
    const shown = rows.filter((r) => imageMatches(r, filters)).sort(IMAGE_SORTS[filters.sort] ?? IMAGE_SORTS.size);
    const total = shown.reduce((sum, r) => sum + r.i.size, 0);
    results.replaceChildren(
      h("p", { class: "subtitle", text: `${shown.length} of ${rows.length} images shown · ${fmtBytes(total)} total` }),
      card(null, table(columns, shown, { href: ({ i }) => imageHref(i), empty: "No images match the filters." })),
    );
  };

  const bar = filterBar(
    [
      { key: "q", type: "search", label: "Search", placeholder: "Tag or ID…" },
      {
        key: "usage", type: "select", label: "Usage",
        options: [
          { value: "all", label: "Any" },
          { value: "used", label: "Used by containers" },
          { value: "unused", label: "Unused" },
        ],
      },
      {
        key: "tag", type: "select", label: "Tag",
        options: [
          { value: "all", label: "Any" },
          { value: "tagged", label: "Tagged" },
          { value: "untagged", label: "Untagged" },
        ],
      },
      {
        key: "size", type: "select", label: "Minimum size",
        options: [
          { value: "all", label: "Any" },
          { value: "100", label: "≥ 100 MB" },
          { value: "500", label: "≥ 500 MB" },
          { value: "1024", label: "≥ 1 GB" },
        ],
      },
      { key: "platform", type: "select", label: "Platform", options: countedOptions("All platforms", countBy(rows, (r) => r.platform)) },
      {
        key: "user", type: "select", label: "Default user",
        options: [
          { value: "all", label: "Any" },
          { value: "root", label: "Root" },
          { value: "nonroot", label: "Non-root" },
        ],
      },
      {
        key: "sort", type: "select", label: "Sort by",
        options: [
          { value: "size", label: "Largest first" },
          { value: "name", label: "Name" },
          { value: "created", label: "Newest first" },
          { value: "usedBy", label: "Most used" },
        ],
      },
    ],
    filters,
    defaults,
    update,
  );

  update();
  return page("Images", null, bar, results);
}

export function imageDetail(report, id) {
  const img = imagesOf(report).find((x) => x.id === id);
  if (!img) return notFound("image", "#/images");

  const inspect = img.inspect;
  const usedBy = containersOf(report).filter((c) => c.image_id === img.id);
  const title = (img.repo_tags ?? [])[0] ?? `<untagged> ${shortId(img.id)}`;

  const head = h(
    "div",
    {},
    link("#/images", "← Images"),
    h("div", { class: "page-head" }, h("h1", { text: title }), img.dangling ? badge("untagged", "warn") : null, img.containers === 0 ? badge("unused", "warn") : null),
    h("p", { class: "subtitle" }, h("span", { class: "mono", text: img.id })),
  );

  if (!inspect) {
    return h("div", {}, head, h("div", { class: "banner", text: `Details unavailable: ${img.inspect_error ?? "unknown error"}` }));
  }

  const cfg = inspect.Config ?? {};
  const layers = inspect.RootFS?.Layers ?? [];

  return h(
    "div",
    {},
    head,
    h(
      "div",
      { class: "tiles" },
      tile("Size", fmtBytes(img.size)),
      tile("Layers", String(layers.length)),
      tile("Platform", `${inspect.Os ?? "—"}/${inspect.Architecture ?? "—"}`),
      tile("Used by", String(usedBy.length), "containers"),
      tile("Created", fmtDate(img.created_at)),
    ),
    h(
      "div",
      { class: "grid" },
      card("Identity", kv([
        ["Tags", chips(img.repo_tags)],
        ["Digests", chips(img.repo_digests)],
        ["Author", inspect.Author],
        ["Comment", inspect.Comment],
        ["Storage driver", inspect.GraphDriver?.Name],
      ])),
      card(
        "Used by containers",
        table(
          [
            { title: "Container", render: (c) => link(containerHref(c), c.name) },
            { title: "State", render: (c) => stateBadge(c.state) },
          ],
          usedBy,
          { empty: "No container uses this image." },
        ),
      ),
    ),
    h(
      "div",
      { class: "grid" },
      card("Configuration", kv([
        ["User", cfg.User ? cfg.User : h("span", { class: "caution", text: "root (default)" })],
        ["Entrypoint", cfg.Entrypoint?.length ? h("code", { text: cfg.Entrypoint.join(" ") }) : "—"],
        ["Command", cfg.Cmd?.length ? h("code", { text: cfg.Cmd.join(" ") }) : "—"],
        ["Working dir", cfg.WorkingDir || "/"],
        ["Exposed ports", chips(Object.keys(cfg.ExposedPorts ?? {}))],
        ["Volumes", chips(Object.keys(cfg.Volumes ?? {}))],
        ["Environment (names only)", chips((cfg.Env ?? []).map((e) => e.split("=")[0]))],
        ["Healthcheck", cfg.Healthcheck?.Test?.length ? h("code", { text: cfg.Healthcheck.Test.join(" ") }) : "none"],
      ])),
      card(
        "Labels",
        table([{ title: "Key", render: ([k]) => h("span", { class: "mono", text: k }) }, { title: "Value", render: ([, v]) => v }], Object.entries(cfg.Labels ?? {}), { empty: "No labels." }),
      ),
    ),
    card("Layers", table([{ title: "Digest", render: (l) => h("span", { class: "mono", text: l }) }], layers, { empty: "No layer information." })),
    rawJson("Raw inspect JSON", inspect),
  );
}

function notFound(kind, back) {
  return h("div", {}, link(back, "← Back"), h("div", { class: "banner", text: `This ${kind} no longer exists or has not been collected yet.` }));
}
