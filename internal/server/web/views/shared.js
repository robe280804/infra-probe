// Helpers shared by every page.

import { h } from "../lib/dom.js";

export const link = (hash, text) => h("a", { href: hash, text });

export function page(title, subtitle, ...children) {
  return h(
    "div",
    {},
    h("div", { class: "page-head" }, h("h1", { text: title })),
    subtitle ? h("p", { class: "subtitle" }, subtitle) : null,
    ...children,
  );
}

export const yesNo = (value, riskyWhenTrue = false) =>
  h("span", { class: value && riskyWhenTrue ? "risk" : "", text: value ? "Yes" : "No" });

export function notFound(kind, back) {
  return h("div", {}, link(back, "← Back"), h("div", { class: "banner", text: `This ${kind} no longer exists or has not been collected yet.` }));
}
