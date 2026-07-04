# Waypoint — Garmin + Claude + Grafana Fitness Tracker

## Context

Personal fitness training tool for Garmin Forerunner 970. Pulls activity, sleep, HRV, and health data from Garmin Connect; stores it in InfluxDB for Grafana visualization; exposes it to Claude for AI coaching and training planning. Built in Go (primary), Python (Garmin auth sidecar only — required due to Cloudflare TLS fingerprinting that blocks Go's net/http on Garmin's SSO endpoints as of March 2026).

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
                              ▼
                    Claude Desktop / Claude Code
                              │
                    (training analysis, planning,
                     natural language queries)

Phase 2: cmd/cli/ ──► calls same internal/ packages as MCP server
Phase 3: cmd/web/ ──► Go HTTP server, optional chat panel
```

---

## Repository Structure

```
waypoint/
├── cmd/
│   ├── mcp-server/          # Phase 1: Go MCP server binary
│   └── cli/                 # Phase 2: Go CLI (melodic-quill)
├── internal/
│   ├── influx/              # InfluxDB client wrapper + query helpers
│   ├── claude/              # Anthropic SDK wrapper, prompt templates
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
├── docker-compose.yml
├── .env.example
├── README.md                # Work in progress, usage, setup instructions
└── LICENSE                  # MIT
```

---

## Phase 1: MCP Server (build first)

### Go MCP server — `cmd/mcp-server/`

Library: `github.com/mark3labs/mcp-go` (most mature Go MCP SDK)

**Tools to expose:**

| Tool | Description |
|------|-------------|
| `get_recent_activities` | Last N activities with type, distance, duration, HR, training load |
| `get_training_load` | ATL (7-day), CTL (42-day), TSB — computed from InfluxDB |
| `get_sleep_summary` | Recent sleep scores, HRV, sleep stages |
| `get_hrv_trend` | HRV readings over time window, baseline comparison |
| `get_weekly_volume` | Distance/time by sport for last N weeks |
| `get_daily_stats` | Body Battery, resting HR, stress, steps for date range |
| `analyze_readiness` | Synthesizes sleep + HRV + TSB → readiness score + explanation |
| `suggest_workout` | Calls Claude with current fitness context → returns workout suggestion |
| `generate_training_plan` | Calls Claude with goals + current fitness → returns N-week plan |

MCP server reads from InfluxDB; two tools (`suggest_workout`, `generate_training_plan`) chain to Claude API.

### Go libraries

| Package | Purpose |
|---------|---------|
| `github.com/mark3labs/mcp-go` | MCP server framework |
| `github.com/anthropics/anthropic-sdk-go` | Claude API |
| `github.com/InfluxCommunity/influxdb3-go/v2` | InfluxDB 3 client |
| `github.com/spf13/viper` | Config (env vars + config file) |

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

Measurement: training_load  (computed + written by Go, not Python)
  Fields: atl_7day, ctl_42day, tsb
```

### Python sync sidecar — `sync/`

- `garminconnect` v0.3.6 with `curl_cffi` Chrome impersonation
- Syncs: activities (last 7 days on first run, incremental after), daily stats, sleep, HRV
- Writes to InfluxDB via `influxdb-client` Python library
- Runs as a Docker container on cron (default: every 30 min)
- Credentials via env vars only (never hardcoded)

### Docker Compose — Phase 1

Services: `influxdb` (3-core), `grafana`, `sync` (Python), `mcp-server` (Go)

Grafana bootstraps with:
- Data source: InfluxDB
- Dashboard: import JSON from grafana.com ID 23245 (Garmin Stats)

### Claude MCP registration

Add to `~/.config/claude/mcp_servers.json` (or Claude Code settings):
```json
{
  "melodic-quill": {
    "command": "waypoint-mcp",
    "env": {
      "INFLUXDB_URL": "http://localhost:8086",
      "INFLUXDB_TOKEN": "...",
      "ANTHROPIC_API_KEY": "..."
    }
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

Claude model: `claude-sonnet-4-6` for analysis/planning (good capability/cost balance). System prompt defines a fitness coach persona with access to user's training history.

---

## Phase 3: Web UI (if warranted)

Go HTTP server (`cmd/web/`) serving:
- Embedded Grafana panel links (iframe or Grafana embedding)
- Chat panel backed by Claude streaming API
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

1. **Now**: `docker-compose up` on local Mac for iteration
2. **Goal**: Deploy to Proxmox (LXC containers) or TrueNAS apps
   - InfluxDB + Grafana as TrueNAS apps (catalog)
   - Python sync + Go MCP server as Proxmox LXC or Docker VM

---

## Public GitHub Repo Baseline

- `README.md`: What it is, prerequisites, quick start, env var reference, work-in-progress note
- `LICENSE`: MIT
- `.env.example`: All env vars documented, no secrets
- `.gitignore`: `.env`, `garmin_tokens.json`, InfluxDB data dirs
- No secrets in code or config defaults

---

## Verification Plan

1. `docker-compose up` → Grafana at :3000, InfluxDB at :8086
2. Python sync runs → data appears in InfluxDB
3. Grafana dashboard 23245 shows real data
4. `mcp-server` binary starts, registers with Claude Desktop
5. Ask Claude: "How was my training last week?" → returns real data from InfluxDB
6. Ask Claude: "Suggest a workout for today given my readiness" → returns AI recommendation
7. Phase 2: `waypoint analyze week` returns markdown report to terminal
