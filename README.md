# infra-probe

A single static Go binary that inspects the server it runs on (Docker
containers and images first, host infrastructure next) and prints a JSON report.

## Quick start

```sh
make build          # bin/infra-probe
./bin/infra-probe scan
./bin/infra-probe version
```

`scan` exits with code 1 if at least one collector failed; the report is
printed anyway.

### Dashboard

```sh
./bin/infra-probe serve            # http://127.0.0.1:8080
```

A read-only web UI (embedded in the binary) listing containers and images,
with filter and full configuration detail. By default it listens on localhost
only; there is no authentication, so do not expose it to the network (a warning
is printed if you try). To reach it on a remote server use an SSH tunnel:
`ssh -L 8080:127.0.0.1:8080 user@server`.

### Export for AI

```sh
./bin/infra-probe export            # infra-probe-<host>-<time>.zip
./bin/infra-probe export -o ctx.zip
```

The same archive is available from the dashboard ("Export for AI" button,
`GET /api/export`). It contains a README, a Markdown `SUMMARY.md`, one JSON file
per container/image/network (with the full inspect payload) and the full report,
so the whole server context can be handed to an AI assistant. Secrets are
removed: environment values, secret-looking labels and keys, password/token flags
in commands and credentials in URLs become `<redacted>`.

## Development

```sh
make help           # list all targets
make check          # vet + lint + race tests, run before committing
make integration    # needs a Docker daemon
```

Requirements: Go (see `go.mod`), [golangci-lint](https://golangci-lint.run/) v2.

## Deployment

Build a static Linux binary with `make build-linux`, copy it to
`/usr/local/bin/infra-probe` and install the unit and timer from
`deployments/systemd/`. See the comments in the unit file for the required
system user.

## Documentation

- [Architecture](docs/architecture.md)
