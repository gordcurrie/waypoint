# Waypoint — Garmin + Claude + Grafana Fitness Tracker

## Context

Personal fitness training tool for Garmin Forerunner 970. Pulls activity, sleep, HRV, and health data from Garmin Connect; stores it in InfluxDB for Grafana visualization; exposes it to Claude (and other LLMs) for AI coaching and training planning. Built in Go (primary), Python (Garmin auth sidecar only — required due to Cloudflare TLS fingerprinting that blocks Go's net/http on Garmin's SSO endpoints as of March 2026).

**Incremental build order**: MCP Server → CLI → Web UI (if warranted). Architecture keeps all three paths open without rewriting.

---

## Architecture Overview

```
Garmin Connect
    │  (unofficial API via python-garminconnect + curl_cffi TLS impersonation)
    ▼
[sync/ Python sidecar] ──────────────────────────────► InfluxDB 3 Core
                                                              │
                              ┌───────────────────────────────┤
                              │                               │
                     [Go MCP server]                    [Grafana]
                     mcp-server/                      dashboards/
                              │
                        stdio or SSE
                              │
                    Claude Desktop / Claude Code
                    (or any MCP-compatible client)
                              │
                    (training analysis, planning,
                     natural language queries)

Phase 2: cmd/cli/ ──► calls same internal/ packages as MCP server
                  └──► uses internal/llm/ provider interface
                       (Claude, Ollama, OpenAI, Gemini, etc.)
Phase 3: cmd/web/ ──► Go HTTP server, optional chat panel
```

**Key design decisions:**
- MCP server is pure data — no LLM calls in Go. Claude is the brain.
- CLI uses `internal/llm` provider interface — swap providers without rewriting.
- Ollama (local, free) is the recommended default for CLI; no API key needed.

---

## Repository Structure

```
waypoint/
├── cmd/
│   ├── mcp-server/          # Phase 1: Go MCP server binary (stdio + http transport)
│   └── cli/                 # Phase 2: Go CLI
├── tools/                   # MCP tool registration (RegisterAll, per-group files, helpers)
├── internal/
│   ├── influx/              # InfluxDB client wrapper + query helpers
│   ├── llm/                 # LLM provider interface + implementations
│   │   ├── provider.go      #   Provider interface
│   │   ├── claude.go        #   Anthropic SDK implementation
│   │   ├── ollama.go        #   Ollama (local) implementation
│   │   └── openai.go        #   OpenAI-compatible implementation
│   ├── garmin/              # Garmin data models (maps to InfluxDB schema)
│   └── analysis/            # Training load (ATL/CTL/TSB), HR zone calcs
├── sync/                    # Python Garmin → InfluxDB sync service
│   ├── sync.py              # Main sync script (activities, daily stats, sleep, HRV)
│   ├── requirements.txt     # garminconnect, influxdb-client
│   └── Dockerfile
├── grafana/
│   ├── provisioning/
│   │   ├── dashboards/fitness.yaml
│   │   └── datasources/influxdb.yaml
│   └── dashboards/          # Dashboard JSONs (import 23245 + custom)
├── docker-compose.yml       # InfluxDB, Grafana, sync sidecar
├── docker-compose.mcp.yml   # Optional: MCP server as SSE service for homelab
├── .env.example
├── README.md
└── LICENSE                  # MIT
```

---

## Phase 1: MCP Server (build first)

### Go MCP server — `cmd/mcp-server/`

Library: `github.com/modelcontextprotocol/go-sdk` (official MCP Go SDK)

Follows `gordcurrie/agent-skills` → `generate-mcp` skill conventions:
- `tools/` package with `RegisterAll`, per-group files, `client_iface.go`
- `tools/helpers.go`: `jsonResult`, `textResult`, `errorResult`

**Transport modes** (controlled by `--transport` flag):
- `stdio` (default) — Claude Desktop/Code spawns it as subprocess. Local dev.
- `http` — Streamable HTTP via `mcp.NewStreamableHTTPHandler`. Homelab deployment.

**Tools to expose (data only — no LLM calls):**

| Tool | Description |
|------|-------------|
| `get_recent_activities` | Last N activities with type, distance, duration, HR, training load |
| `get_training_load` | ATL (7-day), CTL (42-day), TSB — computed on demand from activity data |
| `get_sleep_summary` | Recent sleep scores, HRV, sleep stages |
| `get_hrv_trend` | HRV readings over time window, baseline comparison |
| `get_weekly_volume` | Distance/time by sport for last N weeks |
| `get_daily_stats` | Body Battery, resting HR, stress, steps for date range |
| `analyze_readiness` | Synthesizes sleep + HRV + TSB → readiness score + explanation |

Claude calls these tools and does its own analysis. No chained API calls from Go.

### Training load computation

ATL/CTL/TSB computed **on demand** when `get_training_load` is called:
1. Query `activity` measurement from InfluxDB (last 90 days)
2. Compute exponential moving averages (ATL=7d, CTL=42d, TSB=CTL-ATL)
3. Return result; optionally write back to `training_load` measurement for Grafana

No separate trigger needed. Computation is fast (simple EMA over ~90 data points).

### Go libraries

| Package | Purpose |
|---------|---------|
| `github.com/modelcontextprotocol/go-sdk` | MCP server framework (official SDK) |
| `github.com/InfluxCommunity/influxdb3-go/v2` | InfluxDB 3 client |
| `github.com/spf13/viper` | Config (env vars + config file) |
| `github.com/anthropics/anthropic-sdk-go` | Claude provider (CLI only, optional) |

### InfluxDB Schema

```
Measurement: activity
  Tags: sport, device_id
  Fields: distance_m, duration_s, avg_hr_bpm, max_hr_bpm, calories_kcal,
          elevation_gain_m, avg_pace_s_per_km, training_load_epoc,
          aerobic_te, anaerobic_te

Measurement: daily_stats
  Fields: resting_hr_bpm, hrv_ms, body_battery_max, body_battery_min,
          stress_avg, steps, active_calories, intensity_minutes

Measurement: sleep
  Fields: total_sleep_s, deep_sleep_s, light_sleep_s, rem_sleep_s,
          sleep_score, avg_hrv_ms, avg_spo2_pct, avg_breathing_rate

Measurement: training_load  (computed by Go on demand, written for Grafana)
  Fields: atl_7day, ctl_42day, tsb
```

### Python sync sidecar — `sync/`

- `garminconnect` v0.3.6 with `curl_cffi` Chrome impersonation
- Syncs: activities (last 7 days on first run, incremental after), daily stats, sleep, HRV
- Writes to InfluxDB via `influxdb-client` Python library
- Runs as a Docker container on cron (default: every 30 min)
- Credentials via env vars only (never hardcoded)

### Docker Compose — Phase 1

`docker-compose.yml` services: `influxdb` (3-core), `grafana`, `sync` (Python sidecar)

MCP server is **not** in the default Docker Compose — it runs as a local binary for stdio transport.

For homelab SSE deployment, `docker-compose.mcp.yml` provides an override:
```bash
docker compose -f docker-compose.yml -f docker-compose.mcp.yml up -d
```

Grafana bootstraps with:
- Data source: InfluxDB
- Dashboard: import JSON from grafana.com ID 23245 (Garmin Stats)

### Claude MCP registration

**Local (stdio) — for development:**

Add to `~/.config/claude/mcp_servers.json`:
```json
{
  "waypoint": {
    "command": "/path/to/waypoint-mcp",
    "env": {
      "INFLUXDB_URL": "http://localhost:8086",
      "INFLUXDB_TOKEN": "..."
    }
  }
}
```

**Homelab (HTTP) — for remote deployment:**
```json
{
  "waypoint": {
    "type": "http",
    "url": "http://homelab-ip:8080/mcp"
  }
}
```

---

## Phase 2: CLI Tool

Reuses all `internal/` packages. New surface only.

```
waypoint sync          # trigger Garmin sync now
waypoint analyze week  # AI analysis of last 7 days
waypoint analyze month # AI analysis of last 30 days
waypoint plan [weeks]  # generate training plan
waypoint report        # generate weekly PDF/markdown report
waypoint status        # show current ATL/CTL/TSB + readiness
```

### LLM Provider Interface — `internal/llm/`

```go
type Provider interface {
    Complete(ctx context.Context, system, user string) (string, error)
    Stream(ctx context.Context, system, user string, out io.Writer) error
}
```

Implementations:
- `OllamaProvider` — local, free, no API key. **Recommended default.**
- `ClaudeProvider` — Anthropic SDK, requires `ANTHROPIC_API_KEY`
- `OpenAIProvider` — OpenAI-compatible, requires `OPENAI_API_KEY` (works with OpenAI, Gemini via compat endpoint, etc.)

Config:
```
LLM_PROVIDER=ollama            # default
LLM_PROVIDER=claude
LLM_PROVIDER=openai

OLLAMA_BASE_URL=http://localhost:11434
OLLAMA_MODEL=llama3.3:70b      # 70B minimum recommended for fitness coaching quality
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
OPENAI_BASE_URL=https://api.openai.com/v1  # override for Gemini, etc.
OPENAI_MODEL=gpt-4o
```

**Model quality note:** Fitness coaching (HRV interpretation, periodization, ATL/CTL/TSB) requires a capable model. Minimum: Llama 3.3 70B locally or Claude Sonnet / GPT-4o via API. Smaller models may give poor or unsafe advice.

System prompt defines a fitness coach persona with access to user's training history.

---

## Phase 3: Web UI (if warranted)

Go HTTP server (`cmd/web/`) serving:
- Embedded Grafana panel links (iframe or Grafana embedding)
- Chat panel backed by streaming LLM via `internal/llm` provider
- No external frontend framework needed — HTMX + minimal CSS

Decision gate: revisit after Phase 2. If CLI is sufficient, skip.

---

## Config Design (never paint into a corner)

Single `config.yaml` + env var overrides via Viper. Supports:
- Multiple Garmin accounts (map of user → credentials) — future multi-user
- Multiple InfluxDB buckets per user — future multi-user
- Feature flags: `enable_web`, `enable_mcp`, `enable_cli`

---

## Hosting Path

1. **Now**: `docker-compose up` on local Mac for iteration; MCP server as local binary
2. **Goal**: Deploy to Proxmox (LXC containers) or TrueNAS apps
   - InfluxDB + Grafana + sync sidecar: TrueNAS apps (catalog) or Proxmox Docker VM
   - MCP server: Proxmox LXC or Docker container with SSE transport (`docker-compose.mcp.yml`)
   - Claude connects to homelab MCP via `http://homelab-ip:8080/sse` (LAN/Tailscale)

---

## Public GitHub Repo Baseline

- `README.md`: What it is, prerequisites, quick start, env var reference, work-in-progress note
- `LICENSE`: MIT
- `.env.example`: All env vars documented, no secrets
- `.gitignore`: `.env`, `garmin_tokens.json`, InfluxDB data dirs
- No secrets in code or config defaults

---

## Verification Plan

1. `docker-compose up` → Grafana at :3000, InfluxDB at :8181
2. Python sync runs → data appears in InfluxDB
3. Grafana dashboard 23245 shows real data
4. `waypoint-mcp` binary starts (stdio), registers with Claude Desktop/Code
5. Ask Claude: "How was my training last week?" → calls `get_recent_activities`, returns real data
6. Ask Claude: "What's my readiness today?" → calls `get_training_load` + `analyze_readiness`
7. Phase 2: `waypoint analyze week` returns markdown report to terminal via Ollama
