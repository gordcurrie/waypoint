# Waypoint

> **Work in progress.** Personal fitness tracking and AI coaching tool.

Pulls activity, sleep, HRV, and health data from Garmin Connect. Stores it in InfluxDB for Grafana visualization. Exposes it to Claude via an MCP server for AI-powered training analysis and planning. Also ships a CLI (`waypoint`) that queries the same data and generates coaching analysis via a local or remote LLM.

## Stack

- **Garmin sync**: Python sidecar (`sync/`) using [python-garminconnect](https://github.com/cyberjunky/python-garminconnect)
- **Storage**: InfluxDB 3 Core
- **Visualization**: Grafana (provisioned dashboard at `grafana/provisioning/dashboards/fitness.json`)
- **MCP server**: Go — exposes 9 fitness data tools to Claude (or any MCP client)
- **CLI**: Go — `waypoint` command for AI analysis and planning via Ollama/Claude

## Prerequisites

- Docker + Docker Compose **or** Podman + podman-compose
- Go 1.22+
- Python 3.12+
- Garmin Connect account
- [Ollama](https://ollama.ai) (for CLI; free, local, no API key)

## Quick Start

```bash
cp .env.example .env
# Edit .env with your credentials

# Podman (used in this project)
podman compose up -d

# Docker
docker compose up -d
```

Grafana: http://localhost:3001 (username: `admin`, password: `GRAFANA_ADMIN_PASSWORD` from `.env`)  
InfluxDB: http://localhost:8181

## Garmin Auth (first run)

On first run, interactive MFA is required to get an auth token:

```bash
podman run --rm -it --env-file .env \
  -v waypoint_sync_data:/data \
  localhost/waypoint_sync python auth.py
```

This saves a token to the `waypoint_sync_data` volume. Subsequent syncs use the token until it expires.

If auth'ing on a different host and copying the token into a target volume, copy only the
`garmin_auth` dir — never the whole `/data` dir. `/data` also holds `sync_state.json`; dragging
that along seeds the target with stale watermarks and silently skips the initial `BACKFILL_DAYS`
backfill.

## MCP Server

Build and register with Claude:

```bash
go build -o bin/waypoint-mcp ./cmd/mcp-server/
```

Add to Claude's MCP config (stdio, local dev):
```json
{
  "waypoint": {
    "command": "/path/to/waypoint-mcp",
    "env": {
      "INFLUXDB_URL": "http://localhost:8181",
      "INFLUXDB_TOKEN": "local-dev-token",
      "INFLUXDB_DATABASE": "garmin"
    }
  }
}
```

For homelab HTTP deployment: `./waypoint-mcp --transport=http --addr=0.0.0.0:8080`

**Available tools:** `get_recent_activities`, `get_weekly_volume`, `get_daily_stats`, `get_sleep_summary`, `get_hrv_trend`, `get_training_load`, `get_training_readiness`, `get_scheduled_workouts`, `create_workout`

## CLI

```bash
go build -o bin/waypoint ./cmd/cli/

waypoint status          # ATL/CTL/TSB + latest readiness
waypoint analyze week    # AI analysis of last 7 days
waypoint analyze month   # AI analysis of last 30 days
waypoint plan            # generate a training plan
```

Set `LLM_PROVIDER` and related vars in `.env` (default: `LLM_PROVIDER=ollama`).

## Environment Variables

See `.env.example` for all variables. Key ones:

| Variable | Default | Description |
|----------|---------|-------------|
| `INFLUXDB_URL` | `http://localhost:8181` | InfluxDB 3 Core URL |
| `INFLUXDB_TOKEN` | `local-dev-token` | Any value works with `--without-auth` |
| `INFLUXDB_DATABASE` | `garmin` | InfluxDB database name |
| `LLM_PROVIDER` | `ollama` | `ollama` or `claude` |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama API URL |
| `OLLAMA_MODEL` | — | Model name (e.g. `gemma4:latest`) |
| `ANTHROPIC_API_KEY` | — | Required if `LLM_PROVIDER=claude` |

## Project Structure

```
cmd/mcp-server/   Go MCP server binary
cmd/cli/          Go CLI — waypoint status/analyze/plan
internal/         Shared Go packages (influx, garmin, analysis, llm)
tools/            MCP tool registration
sync/             Python Garmin → InfluxDB sync service
grafana/          Provisioning config + dashboard JSON
```

## Development

```bash
make build   # go build ./...
make test    # go test + pytest
make lint    # ruff + mypy + golangci-lint
```

Or run the individual Go/Python commands directly — see `Makefile` for the full expansion.

CI runs all checks on every push/PR to `main` via `.github/workflows/ci.yml`.

## Deployment (homelab)

```bash
cp deploy.env.example .deploy.env
# Edit .deploy.env — set DEPLOY_HOST, optionally DEPLOY_USER and DEPLOY_PATH

make deploy   # or: bash scripts/deploy.sh
```

Connects via SSH, runs `git pull --ff-only`, rebuilds the sync container image, and restarts all services. `.deploy.env` is gitignored — never committed.

## Homelab Setup (from scratch)

### 1. Create the LXC on Proxmox

In the Proxmox UI (or via the API), create an Ubuntu 24.04 LXC with:

| Setting | Value |
|---------|-------|
| Template | `ubuntu-24.04-standard_24.04-2_amd64.tar.zst` |
| Memory | 2048 MB |
| Cores | 2 |
| Disk | 32 GB (on your preferred storage) |
| Network | Static IP on your LAN bridge (e.g. `192.168.4.25/24`, gw `192.168.4.1`) |
| Features | Nesting enabled (required for Docker inside LXC) |

Start the container and ensure SSH access as root.

### 2. Provision the LXC

Run `scripts/setup-lxc.sh` inside the container. It installs Docker, clones the repo to `/opt/waypoint`, creates `.env` from `.env.example`, and installs + enables the `waypoint` systemd service.

```bash
# From inside the LXC (or via ssh root@<LXC_IP>):
bash <(curl -fsSL https://raw.githubusercontent.com/gordcurrie/waypoint/main/scripts/setup-lxc.sh)

# Or if you've already cloned:
bash /opt/waypoint/scripts/setup-lxc.sh
```

### 3. Configure credentials

```bash
nano /opt/waypoint/.env   # fill in GARMIN_EMAIL, GARMIN_PASSWORD, tokens, etc.
```

### 4. Build images

```bash
cd /opt/waypoint
docker compose -f docker-compose.yml -f docker-compose.homelab.yml build
```

### 5. First-time Garmin auth (interactive MFA required)

```bash
docker run --rm -it --env-file .env \
  -v waypoint_sync_data:/data \
  $(docker compose -f docker-compose.yml -f docker-compose.homelab.yml images -q sync) \
  python auth.py
```

Follow the MFA prompt. Token is saved to the `waypoint_sync_data` volume — subsequent syncs use it automatically.

### 6. Start the stack

```bash
systemctl start waypoint
systemctl status waypoint
```

Grafana: `http://<LXC_IP>:3001` · InfluxDB is localhost-only (`127.0.0.1:8181`) — not reachable from other hosts.

### 7. Wire up Traefik (optional)

Copy `deploy/traefik-waypoint.yml` to your Traefik `conf.d/` directory after updating the LXC IP. Traefik hot-reloads — no restart needed.

### Re-deploying after code changes

From your dev machine:

```bash
make deploy   # SSH → git pull → docker compose build → up -d
```

## Disclaimer

Uses Garmin's unofficial API. For personal use only. Not affiliated with Garmin.
