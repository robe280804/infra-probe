# Architecture

infra-probe is a single static Go binary that runs on the server it inspects,
collects facts about it and prints a JSON report.

```
cmd/infra-probe          entry point: CLI parsing and wiring only
internal/collector       Collector interface (one implementation per data source)
internal/collector/host    host facts
internal/collector/docker  containers + images (full inspect payloads)
internal/report          runs collectors, assembles and encodes the report
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
views/            one file per page
  shared.js         page frame, links, "not found"
  overview.js  containers.js  container-detail.js  images.js  networks.js
```

Dependencies only point downwards: `views` use `data` and `lib`; `data` and `lib`
never import a view. Report data is untrusted, so it only reaches the page through
`textContent`; a Go test fails if any script uses `innerHTML` and similar sinks.

To add a page: create `views/<name>.js`, add a `NAV` entry and a `case` in `app.js`.

## Design rules

- **Collectors are independent.** A failing collector is recorded under
  `errors` and never stops the others.
- **Collectors only read.** No collector changes the state of the host.
- **Secrets are never exported.** Environment variable values are redacted;
  only names are kept.
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
  "results": { "host": {}, "docker": { "containers": [], "images": [] } },
  "errors": { "<collector>": "<message>" }
}
```
