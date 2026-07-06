# Waypoint — Claude Code Instructions

## What this is

Personal fitness tracker: Garmin Connect → InfluxDB 3 → Grafana + Go MCP server + Go CLI.
Full architecture in PLAN.md.

## Non-obvious constraints (don't re-litigate these)

### Python sidecar is required
Garmin sync (`sync/`) must stay Python. Go cannot do it — Cloudflare TLS fingerprinting (JA3)
blocks Go's `net/http` on Garmin SSO as of March 2026. Python uses `curl_cffi` Chrome
impersonation. `github.com/Danny-Dasilva/CycleTLS` exists but has no Garmin client; building
from scratch is not worth it.

### MCP server is pure data — no LLM calls in Go
`cmd/mcp-server/` exposes read-only data tools (activities, sleep, HRV, training load, etc.).
Claude is the brain. Do not add LLM calls, `suggest_workout`, or `generate_training_plan` tools
to the MCP server. Those decisions are made by the LLM consumer, not Go.

### LLM calls belong in `internal/llm/`
The CLI (`cmd/cli/`) uses an LLM provider interface. Ollama is the default (free, local, no API
key). Claude and OpenAI-compatible are optional. See `internal/llm/` structure in PLAN.md.

### MCP SDK: use `github.com/modelcontextprotocol/go-sdk` (official)
Not `mark3labs/mcp-go`. Follow the skill conventions from
`gordcurrie/agent-skills` → `skills/generate-mcp/SKILL.md`. Key patterns:
- `tools/` package with `RegisterAll`, per-group files, `client_iface.go`
- `tools/helpers.go`: `jsonResult`, `textResult`, `errorResult`
- Transport via `--transport` flag: `stdio` (default, local) or `http` (homelab/remote)
- HTTP transport uses `mcp.NewStreamableHTTPHandler`

### MCP server is embedded, not standalone
`cmd/mcp-server/` lives in this monorepo to share `internal/influx`, `internal/garmin`,
`internal/analysis` with `cmd/cli/`. Do not split into a separate repo.

### Training load is computed on demand
`get_training_load` MCP tool queries the `activity` measurement, computes ATL/CTL/TSB
(exponential moving averages: ATL=7d, CTL=42d, TSB=CTL-ATL), and optionally writes back to
the `training_load` measurement for Grafana. No background worker or separate trigger needed.

## Build order (current: Phase 1)

1. Docker Compose — InfluxDB 3 Core + Grafana + sync placeholder ← **next PR**
2. Python sync sidecar (`sync/`)
3. `internal/influx` — InfluxDB client wrapper
4. `internal/garmin` — data models
5. `internal/analysis` — ATL/CTL/TSB computation
6. `tools/` + `cmd/mcp-server/` — MCP server (Phase 1 complete)
7. `internal/llm/` + `cmd/cli/` — CLI (Phase 2)

## Python sync toolchain (`sync/`)

Tools: **ruff** (lint + format), **mypy** (strict type checking), **pytest** + **freezegun** (tests).

Config: `sync/pyproject.toml` — ruff, mypy, and pytest all configured there.
Dev deps: `sync/requirements-dev.txt` (includes `-r requirements.txt`).

Run commands:
```bash
ruff check sync/          # lint
ruff format --check sync/ # format check
mypy --config-file sync/pyproject.toml sync/sync.py
pytest sync/
```

CI: `.github/workflows/ci.yml` — runs all four checks on push/PR to main.

Key conventions:
- Tests in `sync/tests/`, discovered via `testpaths = ["tests"]` in pyproject.toml
- `sync/conftest.py` sets required env vars before `sync` is imported (module-level env reads)
- `conftest.py` patches `DATA_DIR`/`TOKEN_STORE`/`STATE_FILE` to `tmp_path` — no `/data` volume needed in tests
- mypy strict with `disable_error_code = ["import-untyped", "no-untyped-call", "no-untyped-def", "no-any-return"]` to suppress third-party untyped-lib noise
- CI passes `--config-file sync/pyproject.toml` explicitly — mypy won't find it from repo root otherwise

## Go module

`github.com/gordcurrie/waypoint`

Required dependencies (add as needed):
- `github.com/modelcontextprotocol/go-sdk` — MCP server
- `github.com/InfluxCommunity/influxdb3-go/v2` — InfluxDB 3 client
- `github.com/spf13/viper` — config
- `github.com/anthropics/anthropic-sdk-go` — Claude provider (optional, CLI only)

## Skill to invoke for MCP server work

When building `tools/` or `cmd/mcp-server/`, invoke the `generate-mcp` skill:
```
/generate-mcp
```
The skill is at `gordcurrie/agent-skills` → `skills/generate-mcp/SKILL.md`.
Follow its conventions for client interface, helpers, registration pattern, and transport.

## Hosting

Local dev: `docker compose up`, MCP server as local binary (`stdio` transport).
Homelab goal: InfluxDB + Grafana + sync on Proxmox/TrueNAS, MCP server as Docker container
with `--transport=http`, Claude connects to `http://homelab-ip:8080/mcp`.
