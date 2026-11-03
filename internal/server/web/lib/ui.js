// Generic UI building blocks: badges, cards, tiles, tables.

import { h } from "./dom.js";

const ICONS = { ok: "✓", warn: "!", bad: "✕", neutral: "•" };

/** A status pill. Always icon + text so color is never the only signal. */
export function badge(text, level = "neutral") {
  return h(
    "span",
    { class: `badge ${level}` },
    h("span", { class: "badge-icon", "aria-hidden": "true", text: ICONS[level] || ICONS.neutral }),
    text,
  );
}

const STATE_LEVEL = { running: "ok", paused: "warn", restarting: "warn", dead: "bad" };

export function stateBadge(state) {
  return badge(state || "unknown", STATE_LEVEL[state] || "neutral");
}

/** Maps a percentage of a limit to a severity. */
export function severity(pct) {
  if (pct >= 90) return { level: "bad", label: "Critical", icon: ICONS.bad };
  if (pct >= 75) return { level: "warn", label: "High", icon: ICONS.warn };
  return { level: "ok", label: "Normal", icon: ICONS.ok };
}

export function card(title, ...children) {
  return h("section", { class: "card" }, title ? h("h2", { text: title }) : null, ...children);
}

/** A card whose body has a fixed maximum height and scrolls vertically. */
export function scrollCard(title, ...children) {
  return h("section", { class: "card" }, title ? h("h2", { text: title }) : null, h("div", { class: "scroll-body" }, ...children));
}

export function tile(label, value, sub) {
  return h(
    "div",
    { class: "tile" },
    h("div", { class: "tile-label", text: label }),
    h("div", { class: "tile-value" }, value),
    sub ? h("div", { class: "tile-sub" }, sub) : null,
  );
}

/** Definition list from [label, value] pairs; value may be a Node or text. */
export function kv(pairs) {
  const dl = h("dl", { class: "kv" });
  for (const [label, value] of pairs) {
    dl.append(h("dt", { text: label }), h("dd", {}, value === undefined || value === null || value === "" ? "—" : value));
  }
  return dl;
}

/** Table from column definitions: [{title, render(row), num}] and rows. */
export function table(columns, rows, { href, empty = "Nothing to show" } = {}) {
  const thead = h("thead", {}, h("tr", {}, columns.map((c) => h("th", { class: c.num ? "num" : "", text: c.title }))));
  const tbody = h("tbody");

  if (rows.length === 0) {
    tbody.append(h("tr", {}, h("td", { class: "empty", colspan: columns.length, text: empty })));
  }
  for (const row of rows) {
    const target = href ? href(row) : null;
    const tr = h(
      "tr",
      { class: target ? "clickable" : "" },
      columns.map((c) => h("td", { class: c.num ? "num" : "" }, c.render(row))),
    );
    if (target) {
      tr.addEventListener("click", (e) => {
        if (e.target.closest("a")) return; // let real links work normally
        location.hash = target;
      });
    }
    tbody.append(tr);
  }
  return h("div", { class: "table-wrap" }, h("table", {}, thead, tbody));
}

export function chips(items) {
  if (!items || items.length === 0) return "—";
  return h("div", { class: "chips" }, items.map((i) => h("span", { class: "chip", text: i })));
}

/** Collapsible raw JSON view. */
export function rawJson(title, data) {
  return h(
    "details",
    { class: "raw card" },
    h("summary", { text: title }),
    h("pre", { text: JSON.stringify(data, null, 2) }),
  );
}
