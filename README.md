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

A read-only web UI (embedded in the binary) listing the host, the firewall rules, the daemon setup, Compose projects, containers, images, networks and volumes,
with filter and full configuration detail. By default it listens on localhost
only; there is no authentication, so do not expose it to the network (a warning
is printed if you try). To reach it on a remote server use an SSH tunnel:
`ssh -L 8080:127.0.0.1:8080 user@server`.

### What it collects

- **Host** (Linux): distribution, kernel, uptime, load, memory and swap, disks with
  usage, listening ports with the owning process, systemd services and timers, cron
  jobs. On other systems only the basics.
- **Firewall** (Linux): ufw state, default policies and rules; iptables, ip6tables
  and nftables rules; the ports reachable from the network (host listeners and
  Docker-published ports) with what the firewall does with them. A port Docker
  publishes on all interfaces goes through Docker's NAT rules, which ufw never
  sees: the "Security rules" page flags it unless the `DOCKER-USER` chain filters it.
- **Docker**: daemon setup, containers, images, networks, volumes, Compose projects.

Run as an unprivileged user, the probe cannot see which process owns a port (it
needs root or `CAP_SYS_PTRACE` for processes of other users) nor read the users'
crontabs: the report says so in the host `notes`. Likewise the firewall rules
need `CAP_NET_ADMIN` (and the ufw rule files are readable by root only): without
them the probe shows ufw from its world readable configuration and says what is
missing in the firewall `notes`. See the comments in
`deployments/systemd/infra-probe.service`.

### Export for AI

```sh
./bin/infra-probe export            # infra-probe-<host>-<time>.zip
./bin/infra-probe export -o ctx.zip
./bin/infra-probe export -include containers,compose   # only some categories
./bin/infra-probe export -exclude images,volumes       # everything but these
```

The same archive is available from the dashboard ("Export for AI" menu, where
the categories can be ticked, or `GET /api/export?include=containers,compose`).
Categories: `host`, `firewall`, `daemon`, `compose`, `containers`, `images`, `networks`,
`volumes`. What is not selected is removed before the archive is built, so it
appears nowhere in it (not even in `report.json`); the README inside says that
the export is partial. A full export contains a README, a Markdown `SUMMARY.md`, one JSON file
per container/image/network (with the full inspect payload) and the full report,
so the whole server context can be handed to an AI assistant. Secrets are
removed: environment values, secret-looking labels and keys, password/token flags
in commands and credentials in URLs become `<redacted>`.

When the Docker daemon runs on the same machine, the compose files of every
project (found through the labels Compose leaves on the containers) are read and
included with their environment values masked. `.env` files are never read.

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
