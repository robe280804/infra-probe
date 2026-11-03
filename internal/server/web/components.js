// Small, dependency-free UI building blocks.
//
// SECURITY: every value from the report (container names, labels, image tags,
// ...) is untrusted. Elements are built with createElement and filled through
// textContent / text nodes only, never HTML strings. Styles are set through the
// CSSOM (el.style), because the CSP forbids inline style attributes.

const SVG_NS = "http://www.w3.org/2000/svg";

function append(el, children) {
  for (const c of children.flat(Infinity)) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
}

/** Creates an HTML element: h("div", {class: "x", text: "hi"}, child, ...). */
export function h(tag, props, ...children) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(props || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class") el.className = value;
    else if (key === "text") el.textContent = value;
    else if (key === "style") Object.assign(el.style, value);
    else if (key.startsWith("on") && typeof value === "function") el.addEventListener(key.slice(2), value);
    else el.setAttribute(key, value === true ? "" : String(value));
  }
  append(el, children);
  return el;
}

/** Creates an SVG element. */
export function svg(tag, attrs, ...children) {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === undefined || value === null) continue;
    el.setAttribute(key, String(value));
  }
  append(el, children);
  return el;
}

// ---------- formatting ----------

export function fmtBytes(n) {
  if (typeof n !== "number" || !isFinite(n)) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i === 0 || n >= 100 ? 0 : 1)} ${units[i]}`;
}

export function fmtPct(n, digits = 1) {
  return typeof n === "number" && isFinite(n) ? `${n.toFixed(digits)}%` : "—";
}

/** Formats an ISO date; Docker's zero date ("0001-01-01...") means "never". */
export function fmtDate(iso) {
  const d = new Date(iso);
  if (isNaN(d) || d.getFullYear() < 1971) return "—";
  return d.toLocaleString();
}

export function fmtTime(ms) {
  return new Date(ms).toLocaleTimeString();
}

/** Nanoseconds to a short duration string. */
export function fmtNanos(ns) {
  if (!ns) return "—";
  const s = ns / 1e9;
  return s >= 1 ? `${s}s` : `${ns / 1e6}ms`;
}

export function fmtList(arr) {
  return Array.isArray(arr) && arr.length ? arr.join(", ") : "—";
}

export function shortId(id) {
  return String(id || "").replace("sha256:", "").slice(0, 12);
}

// ---------- status ----------

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

// ---------- containers of content ----------

export function card(title, ...children) {
  return h("section", { class: "card" }, title ? h("h2", { text: title }) : null, ...children);
}

/** A card whose body has a fixed maximum height and scrolls vertically. */
export function scrollCard(title, ...children) {
  return h("section", { class: "card" }, title ? h("h2", { text: title }) : null, h("div", { class: "scroll-body" }, ...children));
}

/**
 * A row of filter controls bound to a mutable `filters` object.
 * defs: [{key, label, type: "search" | "select", options?, placeholder?}]
 * Calls onChange() after every change; the bar itself is never rebuilt, so
 * typing keeps its focus.
 */
export function filterBar(defs, filters, defaults, onChange) {
  const controls = {};
  const bar = h("div", { class: "filters", role: "search" });
  const reset = h("button", { type: "button" });

  const activeCount = () => defs.filter((d) => d.key !== "sort" && filters[d.key] !== defaults[d.key]).length;
  const refreshReset = () => {
    const n = activeCount();
    reset.textContent = n ? `Reset filters (${n})` : "Reset filters";
    reset.disabled = n === 0;
  };
  const changed = () => {
    refreshReset();
    onChange();
  };

  for (const d of defs) {
    let control;
    if (d.type === "search") {
      control = h("input", { type: "search", placeholder: d.placeholder, "aria-label": d.label, autocomplete: "off" });
      control.value = filters[d.key];
      control.addEventListener("input", () => {
        filters[d.key] = control.value;
        changed();
      });
    } else {
      // A remembered value may no longer exist (e.g. the container is gone).
      if (!d.options.some((o) => o.value === filters[d.key])) filters[d.key] = defaults[d.key];
      control = h("select", { "aria-label": d.label }, d.options.map((o) => h("option", { value: o.value, text: o.label })));
      control.value = filters[d.key];
      control.addEventListener("change", () => {
        filters[d.key] = control.value;
        changed();
      });
    }
    controls[d.key] = control;
    bar.append(h("label", { class: "filter" }, h("span", { text: d.label }), control));
  }

  reset.addEventListener("click", () => {
    Object.assign(filters, defaults);
    for (const [key, control] of Object.entries(controls)) control.value = filters[key];
    changed();
  });
  bar.append(reset);
  refreshReset();
  return bar;
}

/** Options for a select: "All" first, then each distinct value with its count. */
export function countedOptions(allLabel, counts) {
  const total = [...counts.values()].reduce((a, b) => a + b, 0);
  const rest = [...counts.entries()]
    .sort((a, b) => String(a[0]).localeCompare(String(b[0])))
    .map(([value, n]) => ({ value, label: `${value} (${n})` }));
  return [{ value: "all", label: `${allLabel} (${total})` }, ...rest];
}

export function countBy(items, fn) {
  const counts = new Map();
  for (const item of items) {
    const key = fn(item);
    counts.set(key, (counts.get(key) ?? 0) + 1);
  }
  return counts;
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

// ---------- meters ----------

/** A usage-against-limit meter: the fill color carries severity, with a text label. */
export function meter({ label, value, max, format }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  const sev = severity(pct);
  const valueText = `${format(value)} / ${format(max)} (${pct.toFixed(1)}%)`;

  const fill = h("div", { class: "meter-fill" });
  fill.style.width = `${pct}%`;

  return h(
    "div",
    { class: `meter ${sev.level}` },
    h("div", { class: "meter-head" }, h("span", { class: "meter-label", text: label }), h("span", { class: "meter-value", text: valueText })),
    h(
      "div",
      {
        class: "meter-track",
        role: "meter",
        "aria-label": label,
        "aria-valuemin": 0,
        "aria-valuemax": max,
        "aria-valuenow": value,
        "aria-valuetext": `${valueText}, ${sev.label}`,
      },
      fill,
    ),
    h("div", { class: "meter-state", text: `${sev.icon} ${sev.label}` }),
  );
}

/** Compact meter for table rows. */
export function miniMeter(pct, text) {
  const sev = severity(pct);
  const fill = h("div", { class: "meter-fill" });
  fill.style.width = `${Math.min(100, pct)}%`;
  return h(
    "div",
    { class: `mini meter ${sev.level}` },
    h("div", { class: "meter-track", role: "meter", "aria-valuemin": 0, "aria-valuemax": 100, "aria-valuenow": Math.round(pct), "aria-valuetext": `${text}, ${sev.label}` }, fill),
    h("div", { class: "mini-text", text: `${text} · ${sev.label}` }),
  );
}

/** Ranked horizontal bars (one hue, largest first). items: [{label, value, href}]. */
export function rankedBars(items, format) {
  const max = Math.max(...items.map((i) => i.value), 1);
  const box = h("div", { class: "rank" });
  for (const item of items) {
    const fill = h("div", { class: "rank-fill" });
    fill.style.width = `${(item.value / max) * 100}%`;
    box.append(
      item.href ? h("a", { href: item.href, text: item.label }) : h("span", { text: item.label }),
      h("div", { class: "rank-track" }, fill),
      h("span", { class: "rank-value", text: format(item.value) }),
    );
  }
  return box;
}

// ---------- line chart ----------

/**
 * Time series with a hover crosshair/tooltip and a table view.
 * points: [{t: ms, v: number}], limit: optional y value drawn as a reference line.
 */
export function lineChart({ title, points, format, limit, minMax = 1 }) {
  const W = 640, H = 180;
  const m = { l: 64, r: 24, t: 14, b: 26 };
  const pw = W - m.l - m.r, ph = H - m.t - m.b;

  const maxVal = Math.max(...points.map((p) => p.v), 0);
  const yMax = limit > 0 ? Math.max(limit, maxVal) : Math.max(maxVal * 1.25, minMax);

  const t0 = points[0].t, t1 = points[points.length - 1].t;
  const x = (t) => (t1 === t0 ? m.l + pw : m.l + ((t - t0) / (t1 - t0)) * pw);
  const y = (v) => m.t + ph - (v / yMax) * ph;

  const root = svg("svg", { class: "chart", viewBox: `0 0 ${W} ${H}`, role: "img", "aria-label": `${title} over time` });

  for (const frac of [0, 0.5, 1]) {
    const gy = y(yMax * frac);
    root.append(
      svg("line", { class: frac === 0 ? "axis-line" : "grid-line", x1: m.l, x2: W - m.r, y1: gy, y2: gy }),
      svg("text", { class: "axis-label", x: m.l - 8, y: gy + 4, "text-anchor": "end" }, format(yMax * frac)),
    );
  }
  root.append(
    svg("text", { class: "axis-label", x: m.l, y: H - 6, "text-anchor": "start" }, fmtTime(t0)),
    svg("text", { class: "axis-label", x: W - m.r, y: H - 6, "text-anchor": "end" }, fmtTime(t1)),
  );

  if (limit > 0) {
    root.append(
      svg("line", { class: "limit-line", x1: m.l, x2: W - m.r, y1: y(limit), y2: y(limit) }),
      svg("text", { class: "limit-label", x: W - m.r, y: y(limit) - 4, "text-anchor": "end" }, `limit ${format(limit)}`),
    );
  }

  const coords = points.map((p) => `${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`);
  if (points.length > 1) {
    root.append(
      svg("polygon", { class: "series-area", points: `${x(t0)},${y(0)} ${coords.join(" ")} ${x(t1)},${y(0)}` }),
      svg("polyline", { class: "series-line", points: coords.join(" ") }),
    );
  }
  const last = points[points.length - 1];
  root.append(svg("circle", { class: "dot", cx: x(last.t), cy: y(last.v), r: 4 }));

  // Hover layer: crosshair + tooltip at the nearest sample.
  const crosshair = svg("line", { class: "crosshair", y1: m.t, y2: m.t + ph, visibility: "hidden" });
  const hoverDot = svg("circle", { class: "dot", r: 4, visibility: "hidden" });
  const overlay = svg("rect", { x: m.l, y: m.t, width: pw, height: ph, fill: "transparent" });
  root.append(crosshair, hoverDot, overlay);

  const tip = h("div", { class: "chart-tip" });
  tip.hidden = true;

  overlay.addEventListener("mousemove", (e) => {
    const rect = root.getBoundingClientRect();
    const px = ((e.clientX - rect.left) / rect.width) * W;
    let nearest = points[0];
    for (const p of points) if (Math.abs(x(p.t) - px) < Math.abs(x(nearest.t) - px)) nearest = p;

    crosshair.setAttribute("x1", x(nearest.t));
    crosshair.setAttribute("x2", x(nearest.t));
    hoverDot.setAttribute("cx", x(nearest.t));
    hoverDot.setAttribute("cy", y(nearest.v));
    crosshair.setAttribute("visibility", "visible");
    hoverDot.setAttribute("visibility", "visible");

    tip.textContent = `${fmtTime(nearest.t)} · ${format(nearest.v)}`;
    tip.style.left = `${(x(nearest.t) / W) * 100}%`;
    tip.style.top = `${(y(nearest.v) / H) * 100}%`;
    tip.hidden = false;
  });
  overlay.addEventListener("mouseleave", () => {
    crosshair.setAttribute("visibility", "hidden");
    hoverDot.setAttribute("visibility", "hidden");
    tip.hidden = true;
  });

  const recent = points.slice(-20).reverse();
  const tableView = h(
    "details",
    { class: "table-view" },
    h("summary", { text: "Table view" }),
    table(
      [
        { title: "Time", render: (p) => fmtTime(p.t) },
        { title: title, render: (p) => format(p.v), num: true },
      ],
      recent,
    ),
  );

  return h(
    "div",
    {},
    h("div", { class: "chart-title", text: title }),
    h("div", { class: "chart-wrap" }, root, tip),
    points.length < 2 ? h("div", { class: "muted", text: "Collecting samples… the chart fills in as the page refreshes." }) : null,
    tableView,
  );
}
