// Host page: system, memory, disks, listening ports, services, cron.

import { h } from "../lib/dom.js";
import { fmtBytes, fmtDate, fmtUptime } from "../lib/format.js";
import { badge, card, tile, kv, table, chips, rawJson } from "../lib/ui.js";
import { meter, miniMeter } from "../lib/charts.js";
import { containersOf, containerHref } from "../data/report.js";
import { hostOf, publishedPorts, containersFor, exposure, loadLevel, hostFindings, inodesValid } from "../data/host.js";
import { link, page } from "./shared.js";

const mono = (text) => h("span", { class: "mono", text });

function findingsCard(host) {
  const items = hostFindings(host);
  return card(
    "Attention",
    items.length === 0
      ? badge("No findings", "ok")
      : h("ul", { class: "findings" }, items.map((f) => h("li", {}, badge(f.level === "bad" ? "Risk" : "Warning", f.level), f.text))),
  );
}

function memoryCard(mem) {
  if (!mem) return card("Memory", h("div", { class: "muted", text: "Unavailable." }));
  const used = mem.total - mem.available;
  return card(
    "Memory",
    meter({ label: "RAM in use (without reclaimable cache)", value: used, max: mem.total, format: fmtBytes }),
    mem.swap_total > 0
      ? meter({ label: "Swap in use", value: mem.swap_total - mem.swap_free, max: mem.swap_total, format: fmtBytes })
      : h("p", { class: "muted", text: "No swap configured." }),
  );
}

function filesystemsCard(filesystems) {
  return card(
    "Disks",
    table(
      [
        { title: "Mount point", render: (f) => mono(f.mountpoint) },
        { title: "Device", render: (f) => mono(f.device) },
        { title: "Type", render: (f) => f.type },
        { title: "Size", num: true, render: (f) => fmtBytes(f.total) },
        { title: "Free", num: true, render: (f) => fmtBytes(f.avail) },
        { title: "Used", render: (f) => (f.total > 0 ? miniMeter(f.used_percent, `${f.used_percent.toFixed(1)}%`) : "—") },
        { title: "Inodes free", num: true, render: (f) => (inodesValid(f) ? `${((f.inodes_free / f.inodes_total) * 100).toFixed(0)}%` : "—") },
        { title: "Flags", render: (f) => (f.read_only ? badge("read-only") : null) },
      ],
      filesystems,
      { empty: "No disk found." },
    ),
  );
}

function listenersCard(host, containers) {
  const published = publishedPorts(containers);
  return card(
    "Listening ports",
    table(
      [
        { title: "Port", num: true, render: (l) => String(l.port) },
        { title: "Protocol", render: (l) => l.protocol },
        { title: "Address", render: (l) => mono(l.address) },
        { title: "Reachable from", render: (l) => { const e = exposure(l); return badge(e.text, e.level); } },
        { title: "Process", render: (l) => (l.process ? `${l.process} (${l.pid})` : "—") },
        {
          title: "Container",
          render: (l) => {
            const found = containersFor(l, published);
            return found.length ? h("div", { class: "stack-list" }, found.map((c) => link(containerHref(c), c.name))) : "—";
          },
        },
      ],
      host.listeners ?? [],
      { empty: "Nothing is listening." },
    ),
    h("p", { class: "muted", text: "\"All interfaces\" means any network the machine is on can reach the port, unless a firewall blocks it. Docker-published ports only show here when a proxy process listens on them." }),
  );
}

function systemdCards(systemd) {
  if (!systemd) return [card("Services", h("div", { class: "muted", text: "systemd is not running on this machine." }))];

  const ordered = [...systemd.services].sort((a, b) => (b.active === "failed") - (a.active === "failed") || a.name.localeCompare(b.name));
  return [
    card(
      "Services running or failed",
      table(
        [
          { title: "Unit", render: (u) => mono(u.name) },
          { title: "State", render: (u) => (u.active === "failed" ? badge("failed", "bad") : badge(u.sub, "ok")) },
          { title: "Description", render: (u) => u.description || "" },
        ],
        ordered,
        { empty: "No service." },
      ),
    ),
    card("Enabled at boot", chips(systemd.enabled)),
    card(
      "Timers",
      table(
        [
          { title: "Timer", render: (t) => mono(t.name) },
          { title: "Starts", render: (t) => mono(t.activates) },
        ],
        systemd.timers,
        { empty: "No timer." },
      ),
    ),
  ];
}

function cronCard(cron) {
  return card(
    "Scheduled jobs (cron)",
    table(
      [
        { title: "Schedule", render: (e) => mono(e.schedule) },
        { title: "User", render: (e) => e.user },
        { title: "Command", render: (e) => mono(e.command) },
        { title: "Source", render: (e) => mono(e.source) },
      ],
      cron,
      { empty: "No job found." },
    ),
    h("p", { class: "muted", text: "Secrets in command arguments are masked." }),
  );
}

export function hostPage(report) {
  const host = hostOf(report);
  if (!host.hostname) return page("Host", null, h("div", { class: "banner", text: "No host data was collected." }));

  const sys = host.system;
  const mem = host.memory;
  const root = (host.filesystems ?? []).find((f) => f.mountpoint === "/");
  const loadText = sys ? sys.load_1.toFixed(2) : "—";

  const notes = (host.notes ?? []).map((n) => h("div", { class: "banner", text: n }));

  // Without system details (not Linux) an empty table would read as "nothing
  // listening": say what is known and why the rest is missing.
  if (!sys) {
    return page(
      "Host",
      host.hostname,
      ...notes,
      h("div", { class: "tiles" }, tile("System", host.os, host.arch), tile("CPUs", String(host.num_cpu))),
      rawJson("Raw host JSON", host),
    );
  }

  return page(
    "Host",
    [host.hostname, sys?.pretty_name].filter(Boolean).join(" · "),
    ...notes,
    h(
      "div",
      { class: "tiles" },
      tile("System", sys?.pretty_name ?? host.os, sys?.kernel ? `kernel ${sys.kernel}` : null),
      tile("Uptime", sys ? fmtUptime(sys.uptime_seconds) : "—", sys ? `since ${fmtDate(sys.boot_time)}` : null),
      tile("Load", loadText, sys ? h("span", {}, badge(`${sys.load_5.toFixed(2)} · ${sys.load_15.toFixed(2)}`, loadLevel(sys.load_5, host.num_cpu))) : null),
      tile("CPUs", String(host.num_cpu), sys?.cpu_model),
      tile("Memory", mem ? fmtBytes(mem.total) : "—", mem ? `${fmtBytes(mem.available)} available` : null),
      tile("Root disk", root ? `${root.used_percent.toFixed(0)}%` : "—", root ? `${fmtBytes(root.avail)} free` : null),
    ),
    h("div", { class: "grid" }, findingsCard(host), memoryCard(mem)),
    filesystemsCard(host.filesystems ?? []),
    listenersCard(host, containersOf(report)),
    ...systemdCards(host.systemd),
    cronCard(host.cron ?? []),
    rawJson("Raw host JSON", host),
  );
}
