// Filter bar bound to a mutable filters object, and helpers to build its options.

import { h } from "./dom.js";

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
