// Images list and image detail pages.

import { h } from "../lib/dom.js";
import { fmtBytes, fmtDate, shortId } from "../lib/format.js";
import { badge, stateBadge, card, tile, kv, table, chips, rawJson } from "../lib/ui.js";
import { filterBar, countedOptions, countBy } from "../lib/filters.js";
import { containersOf, imagesOf, containerHref, imageHref } from "../data/report.js";
import { isRoot } from "../data/findings.js";
import { link, page, notFound } from "./shared.js";

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
    {
      title: "Tags",
      render: ({ i }) =>
        h("span", {}, link(imageHref(i), (i.repo_tags ?? []).join(", ") || "<none>"), i.inspect_error ? [" ", badge("inspect failed", "bad")] : null),
    },
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
