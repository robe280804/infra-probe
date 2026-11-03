// Overview page: key numbers, containers needing attention, top memory users.

import { h } from "../lib/dom.js";
import { fmtBytes } from "../lib/format.js";
import { badge, scrollCard, tile, kv, table } from "../lib/ui.js";
import { rankedBars } from "../lib/charts.js";
import { containersOf, imagesOf, networksOf, containerHref } from "../data/report.js";
import { findings } from "../data/findings.js";
import { link, page } from "./shared.js";

export function overview(report) {
  const containers = containersOf(report);
  const images = imagesOf(report);
  const running = containers.filter((c) => c.state === "running");
  const withFindings = containers.map((c) => ({ c, f: findings(c) })).filter((x) => x.f.length > 0);
  withFindings.sort((a, b) => b.f.filter((x) => x.level === "bad").length - a.f.filter((x) => x.level === "bad").length);

  const host = report?.results?.host ?? {};
  const topMemory = running
    .filter((c) => c.stats)
    .sort((a, b) => b.stats.memory_usage - a.stats.memory_usage)
    .slice(0, 8);

  return page(
    "Overview",
    `Host ${host.hostname ?? "—"}`,
    h(
      "div",
      { class: "tiles" },
      tile("Containers", String(containers.length), `${running.length} running`),
      tile("Stopped", String(containers.length - running.length)),
      tile("Images", String(images.length)),
      tile("Networks", String(networksOf(report).length)),
      tile("Unused images", String(images.filter((i) => i.containers === 0).length), "not used by any container"),
      tile("With findings", String(withFindings.length), "containers to review"),
    ),
    h(
      "div",
      { class: "grid overview" },
      scrollCard(
        "Containers needing attention",
        withFindings.length === 0
          ? h("div", { class: "empty", text: "No findings." })
          : table(
              [
                { title: "Container", render: ({ c }) => link(containerHref(c), c.name) },
                { title: "Findings", render: ({ f }) => h("div", { class: "badges" }, f.slice(0, 3).map((x) => badge(x.text, x.level)), f.length > 3 ? badge(`+${f.length - 3} more`) : null) },
              ],
              withFindings,
              { href: ({ c }) => containerHref(c) },
            ),
      ),
      scrollCard(
        "Memory in use (top containers)",
        topMemory.length === 0
          ? h("div", { class: "empty", text: "No running containers with stats." })
          : rankedBars(topMemory.map((c) => ({ label: c.name, value: c.stats.memory_usage, href: containerHref(c) })), fmtBytes),
      ),
      scrollCard("Host", kv(Object.entries(host).map(([k, v]) => [k, String(v)]))),
    ),
  );
}
