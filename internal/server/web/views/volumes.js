// Volumes list and volume detail pages.

import { h } from "../lib/dom.js";
import { fmtBytes, fmtDate, shortId } from "../lib/format.js";
import { badge, stateBadge, card, tile, kv, table, rawJson } from "../lib/ui.js";
import { filterBar, countedOptions, countBy } from "../lib/filters.js";
import { containersOf, volumesOf, containerHref, volumeHref } from "../data/report.js";
import { link, page, notFound } from "./shared.js";

export const defaultVolumeFilters = () => ({
  q: "",
  driver: "all",
  usage: "all",
  kind: "all",
  project: "all",
  sort: "size",
});

const COMPOSE_PROJECT = "com.docker.compose.project";
const NO_PROJECT = "(none)";

// Unknown sizes (-1) sort last.
const VOLUME_SORTS = {
  size: (a, b) => b.size - a.size,
  name: (a, b) => a.name.localeCompare(b.name),
  created: (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at),
  containers: (a, b) => b.containers.length - a.containers.length,
};

const projectOf = (v) => v.labels?.[COMPOSE_PROJECT] ?? NO_PROJECT;
const fmtSize = (n) => (n >= 0 ? fmtBytes(n) : "unknown");

/** Short display name: anonymous volumes have a 64-hex random name. */
export const volumeLabel = (v) => (v.anonymous ? `anonymous ${shortId(v.name)}` : v.name);

function volumeMatches(v, f) {
  const q = f.q.trim().toLowerCase();
  if (q && ![v.name, v.mountpoint, ...v.containers.map((m) => m.container_name)].some((x) => String(x ?? "").toLowerCase().includes(q))) return false;
  if (f.driver !== "all" && v.driver !== f.driver) return false;
  if (f.usage === "used" && v.containers.length === 0) return false;
  if (f.usage === "unused" && v.containers.length > 0) return false;
  if (f.kind === "named" && v.anonymous) return false;
  if (f.kind === "anonymous" && !v.anonymous) return false;
  if (f.project !== "all" && projectOf(v) !== f.project) return false;
  return true;
}

/** Badges describing a volume; always icon + label. */
function volumeFlags(v) {
  return [
    v.containers.length === 0 ? badge("unused", "warn") : null,
    v.anonymous ? badge("anonymous") : null,
    v.driver !== "local" ? badge(`driver ${v.driver}`) : null,
  ];
}

export function volumeList(report, filters) {
  const defaults = defaultVolumeFilters();
  const rows = volumesOf(report);

  const columns = [
    { title: "Name", render: (v) => link(volumeHref(v), volumeLabel(v)) },
    { title: "Size", num: true, render: (v) => fmtSize(v.size) },
    { title: "Containers", render: (v) => (v.containers.length ? v.containers.map((m) => m.container_name).join(", ") : "—") },
    { title: "Project", render: (v) => (projectOf(v) === NO_PROJECT ? "—" : projectOf(v)) },
    { title: "Driver", render: (v) => v.driver },
    { title: "Flags", render: (v) => h("div", { class: "badges" }, volumeFlags(v)) },
    { title: "Created", render: (v) => fmtDate(v.created_at) },
  ];

  const results = h("div");
  const update = () => {
    const shown = rows.filter((v) => volumeMatches(v, filters)).sort(VOLUME_SORTS[filters.sort] ?? VOLUME_SORTS.size);
    const total = shown.reduce((sum, v) => sum + Math.max(v.size, 0), 0);
    const unusedSize = shown.filter((v) => v.containers.length === 0).reduce((sum, v) => sum + Math.max(v.size, 0), 0);
    results.replaceChildren(
      h("p", { class: "subtitle", text: `${shown.length} of ${rows.length} volumes shown · ${fmtBytes(total)} total · ${fmtBytes(unusedSize)} in unused volumes` }),
      card(null, table(columns, shown, { href: volumeHref, empty: "No volumes match the filters." })),
    );
  };

  const bar = filterBar(
    [
      { key: "q", type: "search", label: "Search", placeholder: "Name, path or container…" },
      { key: "driver", type: "select", label: "Driver", options: countedOptions("All drivers", countBy(rows, (v) => v.driver)) },
      {
        key: "usage", type: "select", label: "Usage",
        options: [
          { value: "all", label: "Any" },
          { value: "used", label: "Used by containers" },
          { value: "unused", label: "Unused" },
        ],
      },
      {
        key: "kind", type: "select", label: "Type",
        options: [
          { value: "all", label: "Any" },
          { value: "named", label: "Named" },
          { value: "anonymous", label: "Anonymous" },
        ],
      },
      { key: "project", type: "select", label: "Compose project", options: countedOptions("All projects", countBy(rows, projectOf)) },
      {
        key: "sort", type: "select", label: "Sort by",
        options: [
          { value: "size", label: "Largest first" },
          { value: "name", label: "Name" },
          { value: "created", label: "Newest first" },
          { value: "containers", label: "Most containers" },
        ],
      },
    ],
    filters,
    defaults,
    update,
  );

  update();
  return page("Volumes", null, bar, results);
}

export function volumeDetail(report, name) {
  const vol = volumesOf(report).find((v) => v.name === name);
  if (!vol) return notFound("volume", "#/volumes");

  const containers = containersOf(report);
  const inspect = vol.inspect;

  const head = h(
    "div",
    {},
    link("#/volumes", "← Volumes"),
    h("div", { class: "page-head" }, h("h1", { text: volumeLabel(vol) }), h("div", { class: "badges" }, volumeFlags(vol))),
    h("p", { class: "subtitle" }, h("span", { class: "mono", text: vol.name })),
  );

  const usedBy = card(
    "Used by containers",
    table(
      [
        {
          title: "Container",
          render: (m) => {
            const c = containers.find((x) => x.id === m.container_id);
            return c ? link(containerHref(c), c.name) : m.container_name || m.container_id;
          },
        },
        { title: "State", render: (m) => { const c = containers.find((x) => x.id === m.container_id); return c ? stateBadge(c.state) : "—"; } },
        { title: "Mounted at", render: (m) => h("span", { class: "mono", text: m.destination }) },
        { title: "Access", render: (m) => (m.rw ? badge("read-write") : badge("read-only", "ok")) },
      ],
      vol.containers,
      { empty: "No container mounts this volume." },
    ),
  );

  const config = card("Configuration", kv([
    ["Driver", vol.driver],
    ["Scope", vol.scope],
    ["Host path", vol.mountpoint ? h("span", { class: "mono", text: vol.mountpoint }) : null],
    ["Created", fmtDate(vol.created_at)],
    ["Compose project", projectOf(vol) === NO_PROJECT ? null : projectOf(vol)],
    ["References (daemon)", vol.ref_count >= 0 ? String(vol.ref_count) : "unknown"],
  ]));

  const pairs = (obj) => Object.entries(obj ?? {});
  const keyValue = (title, obj, empty) =>
    card(title, table([{ title: "Key", render: ([k]) => h("span", { class: "mono", text: k }) }, { title: "Value", render: ([, v]) => String(v) }], pairs(obj), { empty }));

  return h(
    "div",
    {},
    head,
    h(
      "div",
      { class: "tiles" },
      tile("Size", fmtSize(vol.size)),
      tile("Containers", String(vol.containers.length)),
      tile("Driver", vol.driver),
      tile("Scope", vol.scope),
    ),
    h("div", { class: "grid" }, config, usedBy),
    h("div", { class: "grid" }, keyValue("Driver options", inspect?.Options, "No options."), keyValue("Labels", vol.labels, "No labels.")),
    inspect ? rawJson("Raw volume JSON", inspect) : null,
  );
}
