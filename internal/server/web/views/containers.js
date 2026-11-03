// Containers list page with its filters.

import { h } from "../lib/dom.js";
import { fmtBytes, fmtPct } from "../lib/format.js";
import { badge, stateBadge, card, table } from "../lib/ui.js";
import { filterBar, countedOptions, countBy } from "../lib/filters.js";
import { miniMeter } from "../lib/charts.js";
import { containersOf, containerHref } from "../data/report.js";
import { findings, portRows, worst } from "../data/findings.js";
import { link, page } from "./shared.js";

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
    {
      title: "Name",
      render: ({ c }) =>
        h(
          "span",
          {},
          link(containerHref(c), c.name),
          // Per-item failures stay visible right in the list.
          c.inspect_error ? [" ", badge("inspect failed", "bad")] : null,
          c.stats_error ? [" ", badge("stats failed", "warn")] : null,
        ),
    },
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
