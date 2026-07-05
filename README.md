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

- Docker + Docker Compose
- Go 1.22+
- Python 3.11+
- Garmin Connect account
- Anthropic API key

## Quick Start

```bash
cp .env.example .env
# Edit .env with your credentials

docker compose up -d
```

Grafana: http://localhost:3000 (admin / see .env)
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

## Disclaimer

Uses Garmin's unofficial API. For personal use only. Not affiliated with Garmin.
