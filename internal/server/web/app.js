"use strict";

// All values coming from the report (container names, labels, image tags...)
// are untrusted. They are only ever written with textContent, never innerHTML.

const state = { report: null, tab: "containers", filter: "", selected: null };

const el = (id) => document.getElementById(id);

function node(tag, text, className) {
  const n = document.createElement(tag);
  if (text !== undefined && text !== null) n.textContent = text;
  if (className) n.className = className;
  return n;
}

function badge(text, cls) {
  return node("span", text, "badge " + (cls || ""));
}

function formatDate(iso) {
  const d = new Date(iso);
  return isNaN(d) ? "" : d.toLocaleString();
}

function formatSize(bytes) {
  if (typeof bytes !== "number") return "";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (bytes >= 1024 && i < units.length - 1) { bytes /= 1024; i++; }
  return bytes.toFixed(i === 0 ? 0 : 1) + " " + units[i];
}

// Column definitions: each cell is built as a DOM node.
const columns = {
  containers: [
    { title: "Name", cell: (c) => node("span", c.name) },
    { title: "Image", cell: (c) => node("span", c.image) },
    { title: "State", cell: (c) => badge(c.state, c.state) },
    { title: "Status", cell: (c) => node("span", c.status) },
    { title: "ID", cell: (c) => node("span", c.id, "mono") },
    { title: "Created", cell: (c) => node("span", formatDate(c.created_at)) },
  ],
  images: [
    { title: "Tags", cell: (i) => node("span", (i.repo_tags || []).join(", ") || "<none>") },
    { title: "Size", cell: (i) => node("span", formatSize(i.size)) },
    { title: "Used by", cell: (i) => node("span", String(i.containers)) },
    {
      title: "Flags",
      cell: (i) => {
        const s = node("span");
        if (i.dangling) s.appendChild(badge("dangling", "dangling"));
        if (i.containers === 0) s.appendChild(badge("unused", "dangling"));
        return s;
      },
    },
    { title: "ID", cell: (i) => node("span", (i.id || "").replace("sha256:", "").slice(0, 12), "mono") },
    { title: "Created", cell: (i) => node("span", formatDate(i.created_at)) },
  ],
};

function items() {
  const docker = (state.report && state.report.results && state.report.results.docker) || {};
  return docker[state.tab] || [];
}

function matches(item, filter) {
  if (!filter) return true;
  // Filter on the visible summary only, not on the full inspect payload.
  const { inspect, ...summary } = item;
  return JSON.stringify(summary).toLowerCase().includes(filter);
}

function renderHost() {
  const host = (state.report.results && state.report.results.host) || {};
  const dl = el("host");
  dl.replaceChildren();
  for (const [k, v] of Object.entries(host)) {
    const box = node("div");
    box.appendChild(node("dt", k));
    box.appendChild(node("dd", String(v)));
    dl.appendChild(box);
  }
}

function renderErrors() {
  const box = el("errors");
  const errors = state.report.errors || {};
  const entries = Object.entries(errors);
  box.hidden = entries.length === 0;
  box.replaceChildren();
  for (const [name, msg] of entries) {
    box.appendChild(node("div", name + ": " + msg));
  }
}

function renderTable() {
  const cols = columns[state.tab];
  const head = el("table").tHead;
  const body = el("table").tBodies[0];

  const tr = document.createElement("tr");
  for (const c of cols) tr.appendChild(node("th", c.title));
  head.replaceChildren(tr);

  const rows = items().filter((it) => matches(it, state.filter));
  body.replaceChildren();

  if (rows.length === 0) {
    const row = document.createElement("tr");
    const td = node("td", "Nothing to show", "empty");
    td.colSpan = cols.length;
    row.appendChild(td);
    body.appendChild(row);
    return;
  }

  for (const it of rows) {
    const row = document.createElement("tr");
    if (state.selected === it.id) row.className = "selected";
    for (const c of cols) {
      const td = document.createElement("td");
      td.appendChild(c.cell(it));
      row.appendChild(td);
    }
    row.addEventListener("click", () => showDetail(it));
    body.appendChild(row);
  }
}

function showDetail(item) {
  state.selected = item.id;
  const title = item.name || (item.repo_tags || [])[0] || item.id;
  el("detail-title").textContent = title;
  el("detail-body").textContent = JSON.stringify(item.inspect || item, null, 2);
  el("detail").hidden = false;
  renderTable();
}

function render() {
  if (!state.report) return;
  const docker = (state.report.results && state.report.results.docker) || {};
  el("generated").textContent = "Updated " + formatDate(state.report.generated_at);
  el("count-containers").textContent = "(" + (docker.containers || []).length + ")";
  el("count-images").textContent = "(" + (docker.images || []).length + ")";
  renderHost();
  renderErrors();
  renderTable();
}

async function load() {
  const button = el("refresh");
  button.disabled = true;
  try {
    const res = await fetch("/api/report", { cache: "no-store" });
    if (!res.ok) throw new Error("HTTP " + res.status);
    state.report = await res.json();
    render();
  } catch (err) {
    const box = el("errors");
    box.hidden = false;
    box.textContent = "Cannot load report: " + err.message;
  } finally {
    button.disabled = false;
  }
}

el("refresh").addEventListener("click", load);

el("filter").addEventListener("input", (e) => {
  state.filter = e.target.value.trim().toLowerCase();
  renderTable();
});

el("tabs").addEventListener("click", (e) => {
  // The click may land on the count <span> inside the button.
  const button = e.target.closest("button");
  const tab = button && button.dataset.tab;
  if (!tab) return;
  state.tab = tab;
  state.selected = null;
  el("detail").hidden = true;
  for (const b of el("tabs").querySelectorAll("button")) {
    b.classList.toggle("active", b.dataset.tab === tab);
  }
  renderTable();
});

load();
