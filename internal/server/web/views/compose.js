// Compose projects list and project detail pages.

import { h } from "../lib/dom.js";
import { fmtDate } from "../lib/format.js";
import { badge, stateBadge, card, tile, kv, table, chips, rawJson } from "../lib/ui.js";
import { filterBar } from "../lib/filters.js";
import { containersOf, networksOf, volumesOf, composeOf, containerHref, networkHref, volumeHref, composeHref } from "../data/report.js";
import { composePorts, composeEnvNames, composeMounts, composeFlags } from "../data/compose.js";
import { link, page, notFound } from "./shared.js";
import { volumeLabel } from "./volumes.js";

export const defaultComposeFilters = () => ({ q: "", state: "all", sort: "name" });

const STATE_LEVEL = { running: "ok", partial: "warn", stopped: "neutral", down: "warn" };
const STATE_TEXT = { down: "down (leftovers only)" };
const stateTag = (p) => badge(STATE_TEXT[p.state] ?? p.state, STATE_LEVEL[p.state] ?? "neutral");

const allContainers = (p) => p.services.flatMap((s) => s.containers).filter((c) => !c.one_off);
const runningCount = (p) => allContainers(p).filter((c) => c.state === "running").length;

const COMPOSE_SORTS = {
  name: (a, b) => a.name.localeCompare(b.name),
  containers: (a, b) => allContainers(b).length - allContainers(a).length,
  // Running projects first, leftovers last.
  state: (a, b) => ["running", "partial", "stopped", "down"].indexOf(a.state) - ["running", "partial", "stopped", "down"].indexOf(b.state),
};

function composeMatches(p, f) {
  const q = f.q.trim().toLowerCase();
  if (q && ![p.name, p.working_dir, ...p.services.map((s) => s.name)].some((v) => String(v ?? "").toLowerCase().includes(q))) return false;
  if (f.state !== "all" && p.state !== f.state) return false;
  return true;
}

/** How many containers are not part of any project (started with docker run). */
const outsideCompose = (report) => {
  const inProject = new Set(composeOf(report).flatMap((p) => p.services.flatMap((s) => s.containers.map((c) => c.id))));
  return containersOf(report).filter((c) => !inProject.has(c.id)).length;
};

export function composeList(report, filters) {
  const defaults = defaultComposeFilters();
  const rows = composeOf(report);

  const columns = [
    { title: "Project", render: (p) => link(composeHref(p), p.name) },
    { title: "State", render: stateTag },
    { title: "Services", num: true, render: (p) => String(p.services.length) },
    { title: "Containers", num: true, render: (p) => `${runningCount(p)} / ${allContainers(p).length} running` },
    { title: "Networks", num: true, render: (p) => String(p.networks.length) },
    { title: "Volumes", num: true, render: (p) => String(p.volumes.length) },
    { title: "Directory", render: (p) => (p.working_dir ? h("span", { class: "mono", text: p.working_dir }) : "—") },
  ];

  const results = h("div");
  const update = () => {
    const shown = rows.filter((p) => composeMatches(p, filters)).sort(COMPOSE_SORTS[filters.sort] ?? COMPOSE_SORTS.name);
    results.replaceChildren(
      h("p", { class: "subtitle", text: `${shown.length} of ${rows.length} projects shown · ${outsideCompose(report)} containers are not part of a project` }),
      card(null, table(columns, shown, { href: composeHref, empty: "No project matches the filters." })),
    );
  };

  const bar = filterBar(
    [
      { key: "q", type: "search", label: "Search", placeholder: "Project, service or directory…" },
      {
        key: "state", type: "select", label: "State",
        options: [
          { value: "all", label: "Any" },
          { value: "running", label: "Running" },
          { value: "partial", label: "Partially running" },
          { value: "stopped", label: "Stopped" },
          { value: "down", label: "Down (leftovers only)" },
        ],
      },
      {
        key: "sort", type: "select", label: "Sort by",
        options: [
          { value: "name", label: "Name" },
          { value: "containers", label: "Most containers" },
          { value: "state", label: "State" },
        ],
      },
    ],
    filters,
    defaults,
    update,
  );

  update();
  return page("Compose projects", null, bar, results);
}

const FILE_LEVEL = { ok: "ok", missing: "warn", skipped: "neutral", invalid: "bad", unreadable: "bad" };
const baseName = (path) => path.split(/[\\/]/).pop();

function filesCard(project) {
  return card(
    "Compose files",
    project.config_changed_since_start
      ? h("div", { class: "banner", text: "A compose file was modified after the containers were created: what runs may differ from what the file says." })
      : null,
    table(
      [
        { title: "File", render: (f) => h("span", { class: "mono", text: f.path }) },
        { title: "Status", render: (f) => badge(f.status, FILE_LEVEL[f.status] ?? "neutral") },
        { title: "Modified", render: (f) => (f.modified_at ? fmtDate(f.modified_at) : "—") },
        { title: "Note", render: (f) => f.error || "" },
      ],
      project.files,
      { empty: project.config_files.length ? "Files were not read." : "No compose file recorded." },
    ),
    h("p", { class: "muted", text: "Compose merges the files in this order: later files override earlier ones. Each file is shown on its own, with environment values masked." }),
  );
}

/** One card per readable file: the services it defines. */
function definitionCards(project) {
  return project.files
    .filter((f) => f.status === "ok")
    .flatMap((f) => {
      const services = Object.entries(f.content.services ?? {}).map(([name, svc]) => ({ name, svc: svc ?? {} }));
      return [
        card(
          `Services defined in ${baseName(f.path)}`,
          table(
            [
              { title: "Service", render: ({ name }) => name },
              { title: "Image / build", render: ({ svc }) => h("span", { class: "mono", text: svc.image ?? (svc.build ? "build" : "—") }) },
              { title: "Ports", render: ({ svc }) => chips(composePorts(svc).map((p) => p.text)) },
              { title: "Restart", render: ({ svc }) => svc.restart ?? "—" },
              { title: "Flags", render: ({ svc }) => h("div", { class: "badges" }, composeFlags(svc).map((x) => badge(x.text, x.level))) },
              { title: "Variables", render: ({ svc }) => chips(composeEnvNames(svc)) },
              { title: "Mounts", render: ({ svc }) => chips(composeMounts(svc).map((m) => `${m.source || "anonymous"} → ${m.target}${m.readOnly ? " (ro)" : ""}`)) },
            ],
            services,
            { empty: "No service defined." },
          ),
        ),
        rawJson(`Sanitized content: ${baseName(f.path)}`, f.content),
      ];
    });
}

export function composeDetail(report, name) {
  const project = composeOf(report).find((p) => p.name === name);
  if (!project) return notFound("project", "#/compose");

  const containers = containersOf(report);
  const networks = networksOf(report);
  const volumes = volumesOf(report);
  const regular = allContainers(project);

  const head = h(
    "div",
    {},
    link("#/compose", "← Compose projects"),
    h("div", { class: "page-head" }, h("h1", { text: project.name }), stateTag(project)),
    project.state === "down"
      ? h("p", { class: "subtitle", text: "No container is left: only networks or volumes remain, usually after `compose down` without `-v`." })
      : null,
  );

  const info = card("Project", kv([
    ["Directory", project.working_dir ? h("span", { class: "mono", text: project.working_dir }) : null],
    ["Compose files", project.config_files.length ? h("div", { class: "stack-list" }, project.config_files.map((f) => h("div", { class: "mono", text: f }))) : null],
    ["Environment file", project.environment_file ? h("span", { class: "mono", text: project.environment_file }) : null],
    ["Compose version", project.version],
  ]), h("p", { class: "muted", text: "Paths are those recorded when the project was started. Files are not read: the environment file may hold secrets." }));

  const servicesCard = card(
    "Services",
    table(
      [
        { title: "Service", render: (s) => s.name },
        {
          title: "Containers",
          render: (s) =>
            h(
              "div",
              { class: "stack-list" },
              s.containers.map((c) => {
                const full = containers.find((x) => x.id === c.id);
                return h("div", { class: "badges" }, full ? link(containerHref(full), c.name) : c.name, stateBadge(c.state), c.one_off ? badge("one-off") : null);
              }),
            ),
        },
        { title: "Running", num: true, render: (s) => `${s.running} / ${s.containers.filter((c) => !c.one_off).length}` },
        { title: "Depends on", render: (s) => chips(s.depends_on.map((d) => (d.condition ? `${d.service} (${d.condition})` : d.service))) },
      ],
      project.services,
      { empty: "No container: this project has no service running or stopped." },
    ),
  );

  const networksCard = card(
    "Networks",
    table(
      [
        {
          title: "Network",
          render: (r) => {
            const n = networks.find((x) => x.name === r.name);
            return n ? link(networkHref(n), r.name) : r.name;
          },
        },
        { title: "Name in compose file", render: (r) => r.logical || "—" },
      ],
      project.networks,
      { empty: "No network." },
    ),
  );

  const volumesCard = card(
    "Volumes",
    table(
      [
        {
          title: "Volume",
          render: (r) => {
            const v = volumes.find((x) => x.name === r.name);
            return v ? link(volumeHref(v), volumeLabel(v)) : r.name;
          },
        },
        { title: "Name in compose file", render: (r) => r.logical || "—" },
        {
          title: "Used",
          render: (r) => {
            const v = volumes.find((x) => x.name === r.name);
            return v ? (v.containers.length ? badge("in use", "ok") : badge("unused", "warn")) : "—";
          },
        },
      ],
      project.volumes,
      { empty: "No volume." },
    ),
  );

  return h(
    "div",
    {},
    head,
    h(
      "div",
      { class: "tiles" },
      tile("Services", String(project.services.length)),
      tile("Containers", String(regular.length), `${runningCount(project)} running`),
      tile("Networks", String(project.networks.length)),
      tile("Volumes", String(project.volumes.length)),
      tile("Compose", project.version || "—"),
    ),
    h("div", { class: "grid" }, info, servicesCard),
    h("div", { class: "grid" }, networksCard, volumesCard),
    filesCard(project),
    ...definitionCards(project),
  );
}
