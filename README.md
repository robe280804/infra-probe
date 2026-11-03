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
