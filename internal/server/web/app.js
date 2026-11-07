// Application shell: routing, sidebar, data loading, auto refresh.

import { h } from "./lib/dom.js";
import { fmtDate } from "./lib/format.js";
import { containersOf, imagesOf, networksOf, volumesOf, composeOf } from "./data/report.js";
import { overview } from "./views/overview.js";
import { containerList, defaultContainerFilters } from "./views/containers.js";
import { containerDetail } from "./views/container-detail.js";
import { imageList, imageDetail, defaultImageFilters } from "./views/images.js";
import { networkList, networkDetail, defaultNetworkFilters } from "./views/networks.js";
import { composeList, composeDetail, defaultComposeFilters } from "./views/compose.js";
import { daemonPage } from "./views/daemon.js";
import { hostPage } from "./views/host.js";
import { exportMenu } from "./views/export-menu.js";
import { volumeList, volumeDetail, defaultVolumeFilters } from "./views/volumes.js";

const MAX_SAMPLES = 120;

const state = {
  report: null,
  error: null,
  // Filters live here, not in the views, so they survive refreshes and navigation.
  filters: { containers: defaultContainerFilters(), images: defaultImageFilters(), networks: defaultNetworkFilters(), volumes: defaultVolumeFilters(), compose: defaultComposeFilters() },
  history: new Map(), // container id -> [{t, mem, cpu}] collected while the page is open
  timer: null,
};

const $ = (id) => document.getElementById(id);

// ---------- history (client-side time series) ----------

function recordHistory(report) {
  for (const c of containersOf(report)) {
    if (!c.stats) continue;
    const t = Date.parse(c.stats.sampled_at) || Date.now();
    const samples = state.history.get(c.id) ?? [];
    // The server caches reports: skip a sample we already have.
    if (samples.length && samples[samples.length - 1].t === t) continue;
    samples.push({ t, mem: c.stats.memory_usage, cpu: c.stats.cpu_percent });
    state.history.set(c.id, samples.slice(-MAX_SAMPLES));
  }
}

// ---------- routing ----------

function parseRoute() {
  const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean).map(decodeURIComponent);
  return { section: parts[0] || "overview", id: parts[1] };
}

const NAV = [
  { section: "overview", label: "Overview" },
  { section: "containers", label: "Containers", count: (r) => containersOf(r).length },
  { section: "images", label: "Images", count: (r) => imagesOf(r).length },
  { section: "networks", label: "Networks", count: (r) => networksOf(r).length },
  { section: "volumes", label: "Volumes", count: (r) => volumesOf(r).length },
  { section: "compose", label: "Compose", count: (r) => composeOf(r).length },
  { section: "host", label: "Host" },
  { section: "daemon", label: "Docker daemon" },
  { label: "Security rules", soon: true },
];

function renderNav(route) {
  const nav = $("nav");
  nav.replaceChildren(
    ...NAV.map((item) => {
      if (item.soon) {
        return h("span", { class: "nav-disabled", "aria-disabled": "true" }, item.label, h("span", { class: "nav-soon", text: "soon" }));
      }
      const count = state.report && item.count ? item.count(state.report) : null;
      return h(
        "a",
        { href: `#/${item.section}`, "aria-current": route.section === item.section ? "page" : null },
        item.label,
        count !== null ? h("span", { class: "nav-count", text: String(count) }) : null,
      );
    }),
  );
}

function renderView(route) {
  const view = $("view");

  if (!state.report) {
    view.replaceChildren(
      state.error
        ? h("div", { class: "banner", text: `Cannot load the report: ${state.error}` })
        : h("p", { class: "muted", text: "Loading…" }),
    );
    return;
  }

  let node;
  switch (route.section) {
    case "containers":
      node = route.id ? containerDetail(state.report, route.id, state.history.get(route.id)) : containerList(state.report, state.filters.containers);
      break;
    case "images":
      node = route.id ? imageDetail(state.report, route.id) : imageList(state.report, state.filters.images);
      break;
    case "networks":
      node = route.id ? networkDetail(state.report, route.id) : networkList(state.report, state.filters.networks);
      break;
    case "compose":
      node = route.id ? composeDetail(state.report, route.id) : composeList(state.report, state.filters.compose);
      break;
    case "host":
      node = hostPage(state.report);
      break;
    case "daemon":
      node = daemonPage(state.report);
      break;
    case "volumes":
      node = route.id ? volumeDetail(state.report, route.id) : volumeList(state.report, state.filters.volumes);
      break;
    default:
      node = overview(state.report);
  }

  const errors = Object.entries(state.report.errors ?? {});
  view.replaceChildren(
    ...(errors.length ? [h("div", { class: "banner" }, errors.map(([name, msg]) => h("div", { text: `${name}: ${msg}` })))] : []),
    node,
  );
}

function render({ keepFocus = false } = {}) {
  const route = parseRoute();
  renderNav(route);
  // Do not rebuild the page under the user's cursor while they use a filter.
  if (keepFocus && document.activeElement && document.activeElement.closest(".filters")) return;
  renderView(route);
}

// ---------- data ----------

async function load({ auto = false } = {}) {
  const button = $("refresh");
  button.disabled = true;
  try {
    const res = await fetch("/api/report", { cache: "no-store" });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    state.report = await res.json();
    state.error = null;
    recordHistory(state.report);
    $("updated").textContent = `Updated ${fmtDate(state.report.generated_at)}`;
  } catch (err) {
    state.error = err.message;
  } finally {
    button.disabled = false;
  }
  render({ keepFocus: auto });
}

function schedule() {
  clearInterval(state.timer);
  const ms = Number($("interval").value);
  if (ms > 0) state.timer = setInterval(() => load({ auto: true }), ms);
}

// ---------- wiring ----------

window.addEventListener("hashchange", () => {
  render();
  $("view").focus();
  window.scrollTo(0, 0);
});

$("export").replaceChildren(exportMenu());
$("refresh").addEventListener("click", () => load());
$("interval").addEventListener("change", schedule);
$("theme").addEventListener("click", () => {
  const root = document.documentElement;
  const dark = root.dataset.theme ? root.dataset.theme === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  root.dataset.theme = dark ? "light" : "dark";
});

render();
load();
schedule();
