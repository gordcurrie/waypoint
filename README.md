# Waypoint

> **Work in progress.** Personal fitness tracking and AI coaching tool.

Pulls activity, sleep, HRV, and health data from Garmin Connect. Stores it in InfluxDB for Grafana visualization. Exposes it to Claude via an MCP server for AI-powered training analysis and planning. Also ships a CLI (`waypoint`) that queries the same data and generates coaching analysis via a local or remote LLM.

## Stack

- **Garmin sync**: Python sidecar (`sync/`) using [python-garminconnect](https://github.com/cyberjunky/python-garminconnect)
- **Storage**: InfluxDB 3 Core
- **Visualization**: Grafana (provisioned dashboard at `grafana/provisioning/dashboards/fitness.json`)
- **MCP server**: Go — exposes 7 fitness data tools to Claude (or any MCP client)
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

**Available tools:** `get_recent_activities`, `get_weekly_volume`, `get_daily_stats`, `get_sleep_summary`, `get_hrv_trend`, `get_training_load`, `get_training_readiness`

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

### Go

```bash
go test ./...
go vet ./...
```

### Python sync sidecar (`sync/`)

```bash
pip install -r sync/requirements-dev.txt

ruff check sync/
ruff format --check sync/
mypy --config-file sync/pyproject.toml sync/sync.py
pytest sync/
```

CI runs all checks on every push/PR to `main` via `.github/workflows/ci.yml`.

## Disclaimer

Uses Garmin's unofficial API. For personal use only. Not affiliated with Garmin.
