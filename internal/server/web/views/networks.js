// Networks list and network detail pages.

import { h } from "../lib/dom.js";
import { fmtDate } from "../lib/format.js";
import { badge, stateBadge, card, tile, kv, table, chips, rawJson } from "../lib/ui.js";
import { filterBar, countedOptions, countBy } from "../lib/filters.js";
import { containersOf, networksOf, containerHref, networkHref } from "../data/report.js";
import { link, page, yesNo, notFound } from "./shared.js";

export const defaultNetworkFilters = () => ({
  q: "",
  driver: "all",
  scope: "all",
  kind: "all",
  usage: "all",
  isolation: "all",
  sort: "name",
});

const NETWORK_SORTS = {
  name: (a, b) => a.name.localeCompare(b.name),
  containers: (a, b) => b.containers.length - a.containers.length,
  created: (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at),
};

function networkMatches(n, f) {
  const q = f.q.trim().toLowerCase();
  if (q && ![n.name, n.id, ...(n.subnets ?? [])].some((v) => String(v).toLowerCase().includes(q))) return false;
  if (f.driver !== "all" && n.driver !== f.driver) return false;
  if (f.scope !== "all" && n.scope !== f.scope) return false;
  if (f.kind === "builtin" && !n.builtin) return false;
  if (f.kind === "custom" && n.builtin) return false;
  if (f.usage === "used" && n.containers.length === 0) return false;
  if (f.usage === "empty" && n.containers.length > 0) return false;
  if (f.isolation === "internal" && !n.internal) return false;
  if (f.isolation === "external" && n.internal) return false;
  return true;
}

/** Badges describing a network; always icon + label. */
function networkFlags(n) {
  return [
    n.builtin ? badge("built-in") : null,
    n.internal ? badge("internal only", "ok") : null,
    n.driver === "host" ? badge("shares host network", "warn") : null,
    !n.builtin && n.containers.length === 0 ? badge("no containers", "warn") : null,
    n.ipv6 ? badge("IPv6") : null,
    n.inspect_error ? badge("inspect failed", "bad") : null,
  ];
}

export function networkList(report, filters) {
  const defaults = defaultNetworkFilters();
  const rows = networksOf(report);

  const columns = [
    { title: "Name", render: (n) => link(networkHref(n), n.name) },
    { title: "Driver", render: (n) => n.driver },
    { title: "Scope", render: (n) => n.scope },
    { title: "Subnets", render: (n) => chips(n.subnets) },
    { title: "Containers", num: true, render: (n) => String(n.containers.length) },
    { title: "Flags", render: (n) => h("div", { class: "badges" }, networkFlags(n)) },
    { title: "ID", render: (n) => h("span", { class: "mono", text: n.id }) },
    { title: "Created", render: (n) => fmtDate(n.created_at) },
  ];

  const results = h("div");
  const update = () => {
    const shown = rows.filter((n) => networkMatches(n, filters)).sort(NETWORK_SORTS[filters.sort] ?? NETWORK_SORTS.name);
    results.replaceChildren(
      h("p", { class: "subtitle", text: `${shown.length} of ${rows.length} networks shown` }),
      card(null, table(columns, shown, { href: networkHref, empty: "No networks match the filters." })),
    );
  };

  const bar = filterBar(
    [
      { key: "q", type: "search", label: "Search", placeholder: "Name, ID or subnet…" },
      { key: "driver", type: "select", label: "Driver", options: countedOptions("All drivers", countBy(rows, (n) => n.driver)) },
      { key: "scope", type: "select", label: "Scope", options: countedOptions("All scopes", countBy(rows, (n) => n.scope)) },
      {
        key: "kind", type: "select", label: "Type",
        options: [
          { value: "all", label: "Any" },
          { value: "builtin", label: "Built-in" },
          { value: "custom", label: "Custom" },
        ],
      },
      {
        key: "usage", type: "select", label: "Usage",
        options: [
          { value: "all", label: "Any" },
          { value: "used", label: "With containers" },
          { value: "empty", label: "Empty" },
        ],
      },
      {
        key: "isolation", type: "select", label: "Isolation",
        options: [
          { value: "all", label: "Any" },
          { value: "internal", label: "Internal only" },
          { value: "external", label: "Reachable from outside" },
        ],
      },
      {
        key: "sort", type: "select", label: "Sort by",
        options: [
          { value: "name", label: "Name" },
          { value: "containers", label: "Most containers" },
          { value: "created", label: "Newest first" },
        ],
      },
    ],
    filters,
    defaults,
    update,
  );

  update();
  return page("Networks", null, bar, results);
}

export function networkDetail(report, id) {
  const net = networksOf(report).find((n) => n.id === id);
  if (!net) return notFound("network", "#/networks");

  const inspect = net.inspect;
  const containers = containersOf(report);

  const head = h(
    "div",
    {},
    link("#/networks", "← Networks"),
    h("div", { class: "page-head" }, h("h1", { text: net.name }), h("div", { class: "badges" }, networkFlags(net))),
    h("p", { class: "subtitle" }, h("span", { class: "mono", text: inspect?.Id ?? net.id })),
  );

  const failure = net.inspect_error ? h("div", { class: "banner", text: `Details unavailable: ${net.inspect_error}` }) : null;

  const attached = card(
    "Attached containers",
    table(
      [
        {
          title: "Container",
          render: (e) => {
            const c = containers.find((x) => x.id === e.container_id);
            return c ? link(containerHref(c), c.name) : e.name || e.container_id;
          },
        },
        { title: "State", render: (e) => { const c = containers.find((x) => x.id === e.container_id); return c ? stateBadge(c.state) : "—"; } },
        { title: "IPv4", render: (e) => e.ipv4 || "—" },
        { title: "IPv6", render: (e) => e.ipv6 || "—" },
        { title: "MAC", render: (e) => e.mac || "—" },
      ],
      net.containers,
      { empty: net.inspect_error ? "Unknown: the network could not be inspected." : "No container is attached to this network." },
    ),
  );

  const config = card("Configuration", kv([
    ["Driver", net.driver],
    ["Scope", net.scope],
    ["Created", fmtDate(net.created_at)],
    ["Internal only", yesNo(net.internal)],
    ["Attachable", yesNo(net.attachable)],
    ["Ingress", yesNo(net.ingress)],
    ["IPv6 enabled", yesNo(net.ipv6)],
    ["IPAM driver", inspect?.IPAM?.Driver],
    ["Subnets", chips(net.subnets)],
    ["Gateways", chips(net.gateways)],
  ]));

  const pairs = (obj) => Object.entries(obj ?? {});
  const keyValue = (title, obj, empty) =>
    card(title, table([{ title: "Key", render: ([k]) => h("span", { class: "mono", text: k }) }, { title: "Value", render: ([, v]) => v }], pairs(obj), { empty }));

  return h(
    "div",
    {},
    head,
    failure,
    h(
      "div",
      { class: "tiles" },
      tile("Containers", String(net.containers.length)),
      tile("Subnets", String(net.subnets.length)),
      tile("Driver", net.driver),
      tile("Scope", net.scope),
    ),
    h("div", { class: "grid" }, config, attached),
    h("div", { class: "grid" }, keyValue("Driver options", inspect?.Options, "No options."), keyValue("Labels", inspect?.Labels, "No labels.")),
    inspect ? rawJson("Raw inspect JSON", inspect) : null,
  );
}
