# Architecture

infra-probe is a single static Go binary that runs on the server it inspects,
collects facts about it and prints a JSON report.

```
cmd/infra-probe          entry point: CLI parsing and wiring only
internal/collector       Collector interface (one implementation per data source)
internal/collector/host    host facts
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
  compose.js        ports, mounts, variable names and flags of a compose service
views/            one file per page
  shared.js         page frame, links, "not found"
  overview.js  containers.js  container-detail.js  images.js  networks.js  volumes.js  daemon.js  compose.js
```

Dependencies only point downwards: `views` use `data` and `lib`; `data` and `lib`
never import a view. Report data is untrusted, so it only reaches the page through
`textContent`; a Go test fails if any script uses `innerHTML` and similar sinks.

To add a page: create `views/<name>.js`, add a `NAV` entry and a `case` in `app.js`.

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
- **Secrets are never exported.** Environment variable values are redacted;
  only names are kept. The zip export (`internal/export`) adds a second,
  collector-independent pass over the whole report: secret-looking keys and
  labels, password/token flags in commands and credentials in URLs are masked.
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

## Report schema

```json
{
  "version": "1.2.3",
  "generated_at": "2026-11-03T10:00:00Z",
  "results": { "host": {}, "docker": { "compose": [], "daemon": {}, "containers": [], "images": [], "networks": [], "volumes": [] } },
  "errors": { "<collector>": "<message>" }
}
```
