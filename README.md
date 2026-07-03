# GateShell Agent

An **optional**, self-hosted binary that runs *on* your own server. It has no
web UI and does not phone home anywhere except the ntfy topic you configure
for alerts. It binds **`127.0.0.1` only** — it is *not* reachable from the
network, so there is no exposed port for anyone to hit. The GateShell app
reaches it over the SSH connection it already has to your server (an
SSH-tunnelled, encrypted, host-key-verified channel), so access requires your
server's SSH credentials — only your app can read the data. The bearer token is
kept as defense-in-depth for other local processes on the box.

This is a standalone, **open-source** Go module — published so you can audit
exactly what runs on your server before you install it. Release binaries are
built by CI and published to this repo's [GitHub Releases](https://github.com/Anilkhanna/gateshell-go/releases);
`gateshell.com` hosts a convenience installer that fetches them. It shares no
code with the GateShell app or website.

## What it does (v1)

1. **Collects** local host metrics + service health on an interval:
   CPU %, memory used/total, disk used/total, load average, uptime, network
   throughput, top processes, and the status of docker/systemd/pm2/cron
   services.
2. **Stores** samples in an embedded SQLite database with tiered retention
   (full resolution for a day, downsampled for longer windows, pruned past
   that — see `internal/store`).
3. **Serves** that data to the GateShell mobile app over a token-authed
   REST + streaming API, so the app can show live/historical server health
   without a cloud backend in between.
4. **Alerts**: evaluates threshold rules (metric over/under a value for a
   duration, or a service flipping up/down) and pushes notifications via
   [ntfy](https://ntfy.sh).

It is **optional**. GateShell's live SSH connection already gives you
real-time terminal access; the agent adds background monitoring and push
alerts for when you're *not* connected. If the agent isn't installed, or
isn't reachable, the app falls back to live SSH-based checks — the agent
is a nice-to-have, not a dependency for the app's core SSH/SFTP features.

## Architecture

```
cmd/gateshell-agent/   CLI entrypoint (cobra): serve | pair | version
internal/config/       flags + env + JSON/TOML file -> Config
internal/collector/    ticks every PollInterval, gathers a Sample
internal/store/        Store interface; memory (default) + sqlite (-tags sqlite)
internal/api/          REST + SSE stream, bearer-token authed
internal/alerts/       threshold rules -> ntfy publisher
internal/pair/         pairing token generation/validation
```

Data flow: `collector` ticks on `PollInterval` → gathers a `Sample` → fans
it out to three sinks: `store.SaveSample` (persistence), `api.Server.
BroadcastSample` (live stream to connected app clients), and `alerts.
Evaluator` (threshold checks → ntfy).

### Storage: two builds

- **Default build** (`go build ./...`, no tags): uses `store.MemoryStore`,
  a dependency-free in-memory store. History does **not** survive a
  restart. This exists so the module — and CI — can build and test fully
  offline with just the standard library + cobra.
- **Release build** (`go build -tags sqlite ./...`): uses
  `store.SQLiteStore`, backed by [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite)
  (a pure-Go SQLite driver — no cgo, no system SQLite dependency). This is
  what `Makefile`'s `build` target and `.goreleaser.yaml` always use.

### API surface

All endpoints except `/healthz` require `Authorization: Bearer <pairing token>`.

| Endpoint | Purpose |
|---|---|
| `GET /healthz` | Unauthenticated liveness probe |
| `GET /api/v1/latest` | Most recent sample |
| `GET /api/v1/metrics?from=&to=` | Historical samples in a unix-second time range |
| `GET /api/v1/services` | Service statuses from the latest sample |
| `GET /api/v1/stream` | Live samples as Server-Sent Events |
| `GET /api/v1/config` | Current config, e.g. `{"pollInterval":"60s"}` (`"0s"` = paused) |
| `PATCH /api/v1/config` | Change the poll interval, e.g. `{"pollInterval":"30s"}` — applied at runtime and persisted |

The **poll interval is app-configurable at runtime** (V1-10): `PATCH
/api/v1/config` with `{"pollInterval":"30s"}` reconfigures the collector
without a restart and writes the value back to the config file so it
survives one. `"0s"` pauses collection; negative values are rejected, and
non-zero values below a 5s minimum are rejected. The initial value still
follows the flags > env > file > default precedence, but because a PATCH
persists to the *file*, a `--poll-interval` flag or `GATESHELL_AGENT_POLL_INTERVAL`
env override would win over the persisted value on the next start —
configure the interval via the file (as `install.sh` does) for app-driven
changes to stick.

`/api/v1/stream` uses Server-Sent Events rather than a WebSocket, to keep
the default build dependency-free (stdlib `net/http` only). SSE is
one-directional, which is all a "push new samples to the app" feed needs;
see the doc comment in `internal/api/api.go` if bidirectional messaging is
ever required.

### What's stubbed vs. real today

This is a **compiling skeleton**, not a finished agent. Real, working code:
`/proc/loadavg` + `/proc/uptime` parsing (Linux), the REST/SSE API routing
and bearer-auth middleware, the SQLite schema + insert/query paths, the
ntfy HTTP publisher, and the threshold-rule evaluation state machine.

Deliberately stubbed (see `TODO` comments at each site) pending a Linux
port of the iOS app's local health parsers: CPU %, memory, disk, network
throughput, top-processes, and every service checker (docker/systemd/pm2/
cron). Alert rule persistence is also stubbed — rules are supplied
in-memory only for now.

## Build & run

```sh
cd agent

# Offline-friendly sanity build (in-memory store, no external deps beyond cobra):
go build ./...
go vet ./...

# Real build, with the durable sqlite store:
make build          # -> bin/gateshell-agent
# equivalent to: go build -tags sqlite -o bin/gateshell-agent ./cmd/gateshell-agent

make test           # runs tests under both build configurations
make vet
make tidy           # tidies go.mod/go.sum for both build configurations
```

Generate a pairing token, then run the server:

```sh
./bin/gateshell-agent pair
# -> prints a token; paste it into the mobile app's pairing screen

./bin/gateshell-agent serve --token <TOKEN> --listen-addr 127.0.0.1:8443
```

Configuration can come from flags, environment variables
(`GATESHELL_AGENT_*`, see `internal/config/config.go`), or a JSON config
file passed via `--config`. Precedence: flags > env > file > defaults.

## Installing on a server

```sh
curl -fsSL https://gateshell.com/dl/install-agent.sh | sh -s -- --token <PAIRING_TOKEN>
```

`install.sh` detects OS/arch, downloads the matching release binary,
installs a systemd unit (`deploy/gateshell-agent.service`), writes a config
file and a secrets env file, then enables and starts the service. It's
idempotent — safe to re-run to upgrade or rotate the token. It targets
systemd-based Linux, which is the agent's primary deployment target; macOS
is supported for local development only (build from source and run the
binary directly, or supervise it with launchd yourself).

## Status

Skeleton stage — scaffolding + interfaces are in place; the metric readers,
service checkers, alert-rule persistence, and release infrastructure
(download host, checksums/signing, versioned releases) are all still to be
built. See `TODO` comments throughout `internal/` for the specific next
steps.
