// Sidebar menu to download the zip export, with a choice of categories.

import { h } from "../lib/dom.js";
import { EXPORT_CATEGORIES, exportUrl } from "../data/export.js";

/** The export menu: a collapsible list of categories and the download link. */
export function exportMenu() {
  const boxes = EXPORT_CATEGORIES.map((c) => {
    const input = h("input", { type: "checkbox", value: c.key, "aria-describedby": `export-hint-${c.key}` });
    input.checked = true;
    return { c, input };
  });

  const link = h("a", { class: "button", download: true });
  const note = h("div", { class: "muted", "aria-live": "polite" });

  const selected = () => boxes.filter((b) => b.input.checked).map((b) => b.c.key);

  const update = () => {
    const keys = selected();
    const url = exportUrl(keys);
    if (url) {
      link.setAttribute("href", url);
      link.removeAttribute("aria-disabled");
      link.textContent = keys.length === boxes.length ? "Download everything (.zip)" : `Download ${keys.length} of ${boxes.length} (.zip)`;
      note.textContent = "";
    } else {
      // A link without href is not focusable and does nothing.
      link.removeAttribute("href");
      link.setAttribute("aria-disabled", "true");
      link.textContent = "Download (.zip)";
      note.textContent = "Select at least one category.";
    }
  };

  const setAll = (checked) => {
    for (const b of boxes) b.input.checked = checked;
    update();
  };

  for (const b of boxes) b.input.addEventListener("change", update);

  const all = h("button", { type: "button", onclick: () => setAll(true), text: "All" });
  const none = h("button", { type: "button", onclick: () => setAll(false), text: "None" });

  update();

  return h(
    "details",
    { class: "export" },
    h("summary", { text: "Export for AI" }),
    h(
      "div",
      { class: "export-body" },
      h("p", { class: "muted", text: "Choose what goes in the zip. Secrets are always redacted." }),
      ...boxes.map(({ c, input }) =>
        h(
          "label",
          { class: "check", title: c.hint },
          input,
          h("span", { text: c.label }),
          h("span", { class: "visually-hidden", id: `export-hint-${c.key}`, text: c.hint }),
        ),
      ),
      h("div", { class: "row" }, all, none),
      link,
      note,
    ),
  );
}
