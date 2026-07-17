# Waypoint

> **Work in progress.** Personal fitness tracking and AI coaching tool.

Pulls activity, sleep, HRV, and health data from Garmin Connect. Stores it in InfluxDB for Grafana visualization. Exposes it to Claude via an MCP server for AI-powered training analysis and planning.

## Stack

- **Garmin sync**: Python sidecar (`sync/`) using [python-garminconnect](https://github.com/cyberjunky/python-garminconnect)
- **Storage**: InfluxDB 3 Core
- **Visualization**: Grafana
- **MCP server**: Go — exposes fitness data tools to Claude
- **CLI**: Go — `waypoint` command for analysis and planning

## Prerequisites

- Docker + Docker Compose **or** Podman + podman-compose
- Go 1.22+
- Python 3.12+
- Garmin Connect account

## Quick Start

```bash
cp .env.example .env
# Edit .env with your credentials

# Podman (used in this project)
podman compose up -d

# Docker
docker compose up -d
```

Grafana: http://localhost:3001 (admin / see .env)
InfluxDB: http://localhost:8181

## Environment Variables

See `.env.example` for all required variables.

## Project Structure

```
cmd/mcp-server/   Go MCP server (Phase 1)
cmd/cli/          Go CLI — waypoint analyze/plan/report (Phase 2)
internal/         Shared Go packages
sync/             Python Garmin → InfluxDB sync service
grafana/          Provisioning and dashboards
```

## Phases

- **Phase 1** (current): MCP server + Grafana dashboards
- **Phase 2**: CLI tool
- **Phase 3**: Web UI (if warranted)

## Development

### Python sync sidecar (`sync/`)

Install dev dependencies:

```bash
pip install -r sync/requirements-dev.txt
```

Run linter (ruff):
```bash
ruff check sync/
ruff format --check sync/
```

Run type checker (mypy):
```bash
mypy --config-file sync/pyproject.toml sync/sync.py
```

Run tests (pytest):
```bash
pytest sync/
```

CI runs all three on every push/PR to `main` via `.github/workflows/ci.yml`.

Config lives in `sync/pyproject.toml` (ruff, mypy, pytest all in one file).

### Interactive Garmin auth

First run (or after token expiry) requires an interactive MFA step:

```bash
podman run --rm -it --env-file .env \
  -v waypoint_sync_data:/data \
  localhost/waypoint_sync python auth.py
```

This saves a token to `/data/garmin_auth`. Subsequent syncs use the token — no MFA required until it expires.

## Disclaimer

Uses Garmin's unofficial API. For personal use only. Not affiliated with Garmin.
