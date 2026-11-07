# Architecture

infra-probe is a single static Go binary that runs on the server it inspects,
collects facts about it and prints a JSON report.

```
cmd/infra-probe          entry point: CLI parsing and wiring only
internal/collector       Collector interface (one implementation per data source)
internal/collector/host    system, memory, disks, listening ports, systemd, cron
internal/collector/firewall  ufw configuration, iptables / ip6tables / nftables rules
internal/collector/docker  daemon setup, containers, images, networks, volumes, Compose projects (grouped from labels)
internal/report          runs collectors, assembles and encodes the report
internal/export          sanitized zip archive of a report (AI context export, by category)
internal/redact          shared secret masking rules (collectors and export)
internal/server          read-only HTTP API + embedded dashboard
internal/version         build metadata injected via -ldflags
test/integration         tests needing a real Docker daemon / compiled binary
deployments/systemd      hardened unit + timer for periodic scans
```

## Dashboard UI (`internal/server/web`)

Plain ES modules, no build step, embedded in the binary with `go:embed`.

```
app.js            shell: routing, sidebar, loading, auto refresh
lib/              generic building blocks, knowing nothing about Docker
  dom.js            h()/svg() element builders (textContent only, never HTML strings)
  format.js         bytes, dates, durations
  ui.js             badge, card, tile, table, key/value list
  filters.js        filter bar bound to a mutable filters object
  charts.js         usage meters, ranked bars, time series line chart
data/             knowledge about the report
  report.js         accessors over the report JSON and page links
  findings.js       security/configuration facts derived from inspect data
  daemon.js         configuration notes derived from the daemon data
  host.js           host findings, ports traced to the containers behind them
  firewall.js       ports reachable from the network and the firewall verdict on each
  compose.js        ports, mounts, variable names and flags of a compose service
views/            one file per page
  shared.js         page frame, links, "not found"
  overview.js  containers.js  container-detail.js  images.js  networks.js  volumes.js  daemon.js  compose.js  host.js  firewall.js
```

Dependencies only point downwards: `views` use `data` and `lib`; `data` and `lib`
never import a view. Report data is untrusted, so it only reaches the page through
`textContent`; a Go test fails if any script uses `innerHTML` and similar sinks.

To add a page: create `views/<name>.js`, add a `NAV` entry and a `case` in `app.js`.

## Host collector

On Linux it reads `/proc`, `/etc` and `/var` through an `fs.FS` (`os.DirFS("/")`), so
the parsers are tested with fake files on any OS, plus `readlink` and `statfs` hooks
and one command, `systemctl`, with fixed read-only arguments. Each part (system,
memory, disks, ports, systemd, cron) is independent: a failure is reported with the
others and the rest is kept. What the probe is not allowed to see (processes of other
users, users' crontabs) is not an error: it goes in `notes`, so a gap is not mistaken
for an empty list. `/proc/net` lists the sockets of the network namespace, but the
owning process is only visible in the probe's own PID namespace: sockets held by a
container or a relay show without process. A mount that does not answer (a hung NFS
share) times out after 5 seconds instead of blocking the scan.

## Firewall collector

ufw is read from its files: `/etc/ufw/ufw.conf` (enabled at boot) and
`/etc/default/ufw` (default policies) are world readable, `/etc/ufw/user*.rules`
are root only; the rules are taken from the `### tuple ###` comments ufw writes
above each rule, which hold the rule as the user typed it. The kernel rules come
from `iptables-save`, `ip6tables-save` and `nft list ruleset`, the only other
commands the probe runs (an allow list in the package refuses anything else).
They need `CAP_NET_ADMIN`: the collector reads `CapEff` in `/proc/self/status`
and, without the capability, does not start them at all and writes a note. A
command that is not installed is skipped; one the kernel refuses (iptables-legacy
also wants `CAP_NET_RAW`) is a note, any other failure an error.

The "reachable from outside" analysis crosses three sections of the report, so it
is not done by the collector: it runs in `web/data/firewall.js` for the dashboard
and in `export/summary_firewall.go` for `SUMMARY.md` (keep the two in step). A host
port is judged by ufw (first matching rule, else the default policy) or, without
ufw, by whether the INPUT path has any rule. A Docker-published port is judged by
the `DOCKER-USER` chain (or the forward chains of your own nftables tables with the
nftables backend), because Docker's NAT sends it through FORWARD, where ufw rules
do not apply; with `"iptables": false` or a rootless daemon it is a plain host port.

## Design rules

- **Collectors are independent.** A failing collector is recorded under
  `errors` and never stops the others.
- **Collectors only read.** No collector changes the state of the host.
- **A partial export drops what is not selected before building anything.** Filtering
  happens on the decoded report, so `report.json`, the summary and the manifest never
  mention an excluded category. Categories are listed in `export.Categories` and
  mirrored by `web/data/export.js` (a test keeps the two in step).
- **Compose files are read defensively.** The path comes from a container label,
  so it is only read if it is absolute, `.yml`/`.yaml`, a regular file under 1 MiB,
  and parses as a compose file (a `services` section). Variables are not
  interpolated, env files are never opened, and environment / build-arg values are
  masked (`${VAR}` references are kept: they are names, not values).
- **Secrets never leave the host.** Collectors redact what they know is secret
  (environment values: only names are kept). On top of that, a single generic
  pass, `redact.Tree`, runs wherever a report is serialized: `Report.WriteJSON`
  (the `scan` command and `/api/report`) and the zip export. It masks
  secret-looking keys and labels, password/token flags in commands and
  credentials in URLs, whatever a collector returns, so a new collector cannot
  forget it. Integration tests check it end to end with a real container.
- **Everything behind an interface is testable.** The Docker collector talks to
  a small `dockerAPI` interface so unit tests run without a daemon.
- **`internal/` keeps the API private.** Nothing outside this module may import
  these packages, so they can change freely.

## Adding a collector

1. Create `internal/collector/<name>/` implementing `collector.Collector`.
2. Add unit tests next to the code (`*_test.go`).
3. Register it in `cmd/infra-probe/main.go`.

## Tests

| Kind        | Location                      | Command                                    |
|-------------|-------------------------------|--------------------------------------------|
| Unit        | next to the code (`*_test.go`) | `make test`                                |
| Integration | `test/integration`            | `make integration` (needs Docker)          |

The integration tests build the binary and start a real container (labelled as a
Compose project, with a compose file in a temp dir) hiding generated secrets in its
environment, command, labels and compose file. `scan`, `export` (full and partial)
and `serve` (`/api/report`, `/api/export`) must not output any of them.
## Report schema

```json
{
  "version": "1.2.3",
  "generated_at": "2026-11-03T10:00:00Z",
  "results": { "host": {}, "firewall": {}, "docker": { "compose": [], "daemon": {}, "containers": [], "images": [], "networks": [], "volumes": [] } },
  "errors": { "<collector>": "<message>" }
}
```
