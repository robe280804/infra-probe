// Charts: usage meters, ranked bars and the time series line chart.

import { h, svg } from "./dom.js";
import { fmtTime } from "./format.js";
import { severity, table } from "./ui.js";

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
