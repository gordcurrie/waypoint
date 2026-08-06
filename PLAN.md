# Waypoint — Garmin + Claude + Grafana Fitness Tracker

## Context

Personal fitness training tool for Garmin Forerunner 970. Pulls activity, sleep, HRV, and health data from Garmin Connect; stores it in InfluxDB for Grafana visualization; exposes it to Claude (and other LLMs) for AI coaching and training planning. Built in Go (primary), Python (Garmin auth sidecar only — required due to Cloudflare TLS fingerprinting that blocks Go's net/http on Garmin's SSO endpoints as of March 2026, see CLAUDE.md).

**Status: Phase 1 (MCP server), Phase 2 (CLI), and automatic Garmin API drift detection
(#68) done.** Phase 3 (web UI) is an open decision gate, not started. See "Deferred / open
investigations" below for what's next.

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
                     cmd/mcp-server/                   grafana/provisioning/
                              │
                        stdio or HTTP
                              │
                    Claude Desktop / Claude Code
                    (or any MCP-compatible client)
                              │
                    (training analysis, planning,
                     natural language queries)

cmd/cli/  ──► calls same internal/ packages as MCP server
          └──► uses internal/llm/ provider interface
               (Ollama default, Claude optional, OpenAI stubbed)

cmd/web/  ──► not started — see Phase 3 decision gate
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
│   ├── mcp-server/          # Go MCP server binary (stdio + http transport)
│   └── cli/                 # Go CLI (status, analyze, plan)
├── tools/                   # MCP tool registration: register.go + per-group files
│                             # (activities, health, training, fitness, splits,
│                             #  workouts, exercises), client_iface.go, helpers.go
├── internal/
│   ├── influx/               # InfluxDB client wrapper + query helpers
│   ├── llm/                  # LLM provider interface + implementations
│   │   ├── provider.go       #   Provider interface + selection by LLM_PROVIDER
│   │   ├── claude.go         #   Anthropic SDK implementation
│   │   └── ollama.go         #   Ollama (local) implementation
│   ├── garmin/                # Garmin data models (maps to InfluxDB schema)
│   │   └── exercises/         # go:embed'd exercise catalog (catalog.json/.go)
│   └── analysis/              # Training load (ATL/CTL/TSB), HR zone calcs
├── sync/                     # Python Garmin → InfluxDB sync sidecar
│   ├── sync.py                # Main sync loop (all sync_* functions, SYNC_FUNCS)
│   ├── auth.py                # Standalone login helper (seeds MFA token)
│   ├── inspect_api.py         # Manual live-capture + schema-validation tool
│   ├── schema_validate.py     # Validates a garminconnect response against its schema
│   ├── schemas/                # JSON Schema per garminconnect method (see schemas/README.md)
│   └── requirements.txt
├── grafana/
│   └── provisioning/
│       ├── dashboards/fitness.json + fitness.yaml
│       └── datasources/influxdb.yaml
├── scripts/
│   ├── generate_exercise_catalog.py   # regenerates internal/garmin/exercises/catalog.json
│   ├── deploy.sh                      # homelab deploy helper
│   └── setup-lxc.sh                   # Proxmox LXC provisioning
├── deploy/traefik-waypoint.yml        # Traefik routing for homelab
├── docker-compose.yml                 # InfluxDB, Grafana, sync sidecar (local dev)
├── docker-compose.mcp.yml             # Override: MCP server as HTTP service
├── docker-compose.homelab.yml         # Homelab deployment overrides
├── .env.example
├── README.md
└── LICENSE                            # MIT
```

---

## Phase 1: MCP Server ✓ done

### Go MCP server — `cmd/mcp-server/`

Library: `github.com/modelcontextprotocol/go-sdk` (official MCP Go SDK)

Follows `gordcurrie/agent-skills` → `generate-mcp` skill conventions:
- `tools/` package with `RegisterAll`, per-group files, `client_iface.go`
- `tools/helpers.go`: `jsonResult`, `textResult`, `errorResult`

**Transport modes** (controlled by `--transport` flag):
- `stdio` (default) — Claude Desktop/Code spawns it as subprocess. Local dev.
- `http` — Streamable HTTP via `mcp.NewStreamableHTTPHandler`. Homelab deployment.

**Tools (data only — no LLM calls):** 15 read-only tools plus one write (`create_workout`,
always queues an upload) — see `tools/register.go` for the authoritative, current list
grouped by file (`activities.go`, `health.go`, `training.go`, `fitness.go`, `splits.go`,
`workouts.go`, `exercises.go`). Full per-tool descriptions are in the `waypoint` MCP
server's own instructions (surfaced to Claude at connect time), not duplicated here to
avoid drift.

Claude calls these tools and does its own analysis. No chained LLM calls from Go.

### Training load computation

ATL/CTL/TSB computed **on demand** when `get_training_load` is called:
1. Query `activity` measurement from InfluxDB (fetches warmup history: `ctlDays × 3 = 126 days`)
2. Compute exponential moving averages (ATL=7d, CTL=42d, TSB=CTL-ATL)
3. Return `window_days` results (default 42); optionally write back to `training_load` measurement for Grafana

No separate trigger needed. Computation is fast (simple EMA loop).

### Go libraries

| Package | Purpose |
|---------|---------|
| `github.com/modelcontextprotocol/go-sdk` | MCP server framework (official SDK) |
| `github.com/spf13/viper` | Config (env vars + config file) |
| `github.com/anthropics/anthropic-sdk-go` | Claude provider (CLI only, optional) |

InfluxDB 3 Core HTTP API (`/api/v3/query_sql`, `/api/v3/write_lp`) is called directly via stdlib `net/http` + `encoding/json` — no SDK needed. `influxdb3-go/v2` was evaluated and dropped (17 transitive deps for no benefit over raw HTTP).

### InfluxDB Schema

```
Measurement: activity
  Tags:   sport
  Fields: activity_id, distance_m, duration_s, avg_hr_bpm, max_hr_bpm,
          calories_kcal, elevation_gain_m, avg_speed_m_s, training_load,
          aerobic_te, anaerobic_te, vo2max
  Running extras (sport=running/*):
          cadence_avg_spm, ground_contact_time_ms, vertical_oscillation_mm,
          stride_length_mm, vertical_ratio_pct, avg_power_w

Measurement: daily_stats
  Fields: steps, resting_hr_bpm, body_battery_max, body_battery_min,
          stress_avg, active_calories, total_calories, floors_ascended,
          vigorous_intensity_min, moderate_intensity_min

Measurement: sleep
  Fields: total_sleep_s, deep_sleep_s, light_sleep_s, rem_sleep_s, awake_s,
          sleep_score, avg_hrv_ms, avg_spo2_pct, avg_breathing_rate, avg_stress

Measurement: hrv
  Fields: weekly_avg_ms, last_night_ms, last_5min_high_ms,
          status (2=BALANCED 1=UNBALANCED 0=POOR)

Measurement: training_readiness
  Fields: score, sleep_score, recovery_time_h, acw_pct

Measurement: training_status
  Fields: status_num (5=Peaking → 0=Overreaching), vo2max_running,
          vo2max_cycling, fitness_age

Measurement: performance  (VO2 max / fitness age per day)
  Fields: vo2max, fitness_age

Measurement: lactate_threshold  (most recent LT test result)
  Fields: lt_hr_bpm, lt_pace_s_per_km

Measurement: respiration
  Fields: avg_waking_brpm, avg_sleep_brpm, highest_brpm, lowest_brpm

Measurement: activity_lap  (per-lap splits, one point per lap)
  Tags:   activity_id
  Fields: lap_index, distance_m, duration_s, avg_hr_bpm, max_hr_bpm,
          avg_speed_m_s, avg_cadence_spm, avg_power_w, elevation_gain_m

Measurement: activity_hr_zones  (time-in-zone per activity)
  Tags:   activity_id
  Fields: z1_s, z2_s, z3_s, z4_s, z5_s

Measurement: scheduled_workout  (Garmin Connect calendar entries: manual workouts
                                  and coach/adaptive-training-plan items)
  Tags:   scheduled_id, sport
  Fields: workout_id, name, duration_s

Measurement: training_plan_task  (per-day target detail from the active adaptive
                                   coach training plan — duration/distance/pace-or-HR
                                   target and rest-day flag that scheduled_workout's
                                   calendar items don't carry; 14-day lookahead,
                                   always re-synced fresh, no watermark)
  Tags:   training_plan_id
  Fields: name, description, duration_s, distance_m, rest_day (0.0/1.0),
          workout_phrase, phase

Measurement: training_load  (computed by Go on demand, written for Grafana)
  Fields: atl_7day, ctl_42day, tsb
```

Authoritative source is `sync.py`'s `Point(...)` calls and `sync/schemas/*.schema.json`
(one schema per `garminconnect` method, documenting the real API shape each field is
derived from) — re-check there before trusting this table if it's been a while.

### Python sync sidecar — `sync/`

- `garminconnect` v0.3.6 with `curl_cffi` Chrome impersonation
- Syncs all measurements above (see `sync.py`'s `SYNC_FUNCS` list for the current,
  authoritative set of `sync_*` functions, run in order every cycle)
- First run backfills `BACKFILL_DAYS` (default 90); incremental after that via a
  per-measurement watermark in `/data/sync_state.json` (some feeds — scheduled
  workouts, training plan — have no stable identity to watermark against and just
  re-sync a rolling window fresh every cycle instead)
- Auth tokens cached in `/data/garmin_auth` (Docker volume); survives container restarts
- Writes via `influxdb3-python`; skips points with no data (all fields None)
- Runs every 30 min (configurable via `SYNC_SCHEDULE=*/N * * * *`)
- Credentials via env vars only (never hardcoded)

### Garmin API field verification — `sync/schemas/` + `inspect_api.py`

Every `garminconnect` method `sync.py` calls has a hand-derived JSON Schema in
`sync/schemas/` (field names, nesting, types — no real account data, see
`sync/schemas/README.md` for why). `sync/inspect_api.py <method> <date>`, run inside
the sync container, captures a live response and validates it against that schema,
exiting 1 on mismatch. This is currently a **manual** check — CLAUDE.md's "verify
before writing" rule depends on someone remembering to run it. See "Next" below for
making this automatic.

### Docker/Podman Compose

`docker-compose.yml` services: `influxdb` (3-core), `grafana`, `sync` (Python sidecar). Works with `docker compose` or `podman-compose`.

MCP server is **not** in the default compose stack — it runs as a local binary for stdio transport.

For homelab HTTP deployment:
```bash
# Docker
docker compose -f docker-compose.yml -f docker-compose.mcp.yml -f docker-compose.homelab.yml up -d

# Podman
podman-compose -f docker-compose.yml -f docker-compose.mcp.yml -f docker-compose.homelab.yml up -d
```
`docker-compose.homelab.yml` adds Traefik routing (`deploy/traefik-waypoint.yml`); `scripts/deploy.sh` and `scripts/setup-lxc.sh` automate the Proxmox LXC path.

Grafana bootstraps with:
- Data source: InfluxDB (provisioned via `grafana/provisioning/datasources/influxdb.yaml`, uid=`garmin-influxdb`, InfluxQL, db=`garmin`)
- Dashboard: `grafana/provisioning/dashboards/fitness.json`

### Claude MCP registration

**Local (stdio) — for development:**

Add to `~/.config/claude/mcp_servers.json`:
```json
{
  "waypoint": {
    "command": "/path/to/waypoint-mcp",
    "env": {
      "INFLUXDB_URL": "http://localhost:8181",
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

## Phase 2: CLI Tool ✓ done

Reuses all `internal/` packages.

```
waypoint status        # ATL/CTL/TSB + latest readiness
waypoint analyze week  # AI analysis of last 7 days
waypoint analyze month # AI analysis of last 30 days
waypoint plan          # generate a training plan
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
- `openai` — **stubbed only**, `provider.go` returns an error at selection time
  (`LLM_PROVIDER=openai is not yet implemented`). No `openai.go` implementation exists yet.

Config:
```
LLM_PROVIDER=ollama            # default
LLM_PROVIDER=claude

OLLAMA_BASE_URL=http://localhost:11434
OLLAMA_MODEL=llama3.3:70b      # any capable model
ANTHROPIC_API_KEY=sk-ant-...
```

**Model quality note:** Fitness coaching (HRV interpretation, periodization, ATL/CTL/TSB) benefits from a capable model. Smaller models (≤7B) may give shallow or low-quality advice.

System prompt defines a fitness coach persona with access to user's training history.

---

## Automatic Garmin API drift detection (#68) ✓ done

**Problem**: CLAUDE.md documents 5+ separate bugs from Garmin silently changing/misdocumenting
API field names or shapes (`acwRatio`, `training_status` structure, `sync_lactate_threshold`,
`hrvStatus`, lap fields, `fitnessAge`). `sync/schemas/` + `inspect_api.py` (#57/#63) made
verification fast, but it was still a **manual** step someone had to remember to run — nothing
caught drift on a method already in production use between checks.

**Design**: `sync/drift_check.py` wraps the logged-in `Garmin` client (`wrap(garmin) -> Garmin`,
a thin proxy whose `__getattr__` intercepts calls to any method in
`schema_validate.METHOD_SCHEMA`) so schema-covered calls the sync loop already makes get
validated against their schema as a side effect — no separate polling job or schedule.

- **Off by default.** `DRIFT_CHECK_ENABLED` (default `false`) gates whether `sync.py` even
  calls `drift_check.wrap()` (`_login_and_wrap()`, called after initial login and after
  auth-expiry re-login). This validates one specific account's data and can alert to one
  specific webhook — not behavior every clone of this repo should get unasked. No separate
  frequency knob — once enabled, every schema-covered call is checked, same cadence as
  `SYNC_SCHEDULE`. That's deliberate: validation makes no extra Garmin API calls (it checks a
  response `sync.py` already fetched for real syncing), so there's no rate-limit/cost reason
  to throttle it independently — the only cost of not throttling is a repeated `ERROR` log
  line on a persistent drift, which is arguably useful signal, not noise. Add a throttle later
  if that log volume actually becomes a real problem, not preemptively.
- Validation failure never raises — logs `ERROR` and attempts an alert. A schema bug never
  breaks real syncing: the whole validate+alert block is wrapped in `try/except Exception`,
  so even an unexpected failure inside drift-checking itself (corrupt schema file, disk full)
  can't propagate out of the wrapped Garmin call and cost the caller its real data for that
  cycle (`run_sync`'s per-function `except Exception` would otherwise silently eat the whole
  `sync_*` function's work, not just the drift check — caught in review before merge).
- Falsy responses (`None`/`{}`/`[]`) skip validation entirely — every `sync_*` call site
  already treats those as "no data today" via its own `or {}`/`if raw:` guard, not an error;
  validating the raw response before that guard runs was a false positive found in review.
- **Two independent checks per call**, alerted/deduped separately by a `kind` tag
  (`"mismatch"` vs `"new_fields"`) so one doesn't suppress the other on the same method/day:
  - `schema_validate.validate()` — a field's type/nesting no longer matches the schema.
    Something's actually broken, or the schema itself needs loosening (a field going
    null in a state the original derivation never sampled has been the pattern for every
    real alert so far — see `sync/schemas/README.md`'s per-schema bug notes).
  - `schema_validate.find_new_fields()` — `additionalProperties: true` throughout
    `sync/schemas/` means Garmin adding a field never fails `validate()` (not a breaking
    change), but that also means new fields were previously invisible. This recursively
    walks every object node the schema declares `"properties"` for (resolving `$ref`,
    e.g. the shared `vo2max.schema.json` defs) and reports any instance key not declared
    there. Object nodes keyed only by `patternProperties` (e.g.
    `metricsTrainingLoadBalanceDTOMap`, keyed by device ID) are matched against the
    pattern instead of flagged — a new device ID isn't "a new field," there's no fixed
    field list to compare a dict key against in the first place.
- Per-method alert state (`{method: {kind: last_alerted_date}}`) persists in
  `/data/drift_alert_state.json` (separate from `sync_state.json` so a bug here can't corrupt
  sync watermarks). Alerts dedupe to once per method+kind per calendar day; a failed send
  does *not* mark the day as alerted, so a transient webhook outage retries on the next check
  rather than silently going quiet until tomorrow.
- Alert transport: POST JSON `{method, kind, date, errors}` to `DRIFT_ALERT_WEBHOOK_URL` (an
  n8n webhook that routes to Telegram, matching how other personal notifications are already
  wired) via stdlib `urllib.request` with a context-managed response, 5s timeout, wrapped in
  try/except, returns success/failure. Env var optional; unset = log-only, no alert sent.
- `schema_validate.validate()`'s schema-file load is `functools.cache`d — once enabled it's
  called many times per sync run instead of once per manual `inspect_api.py` invocation, so
  the earlier uncached disk read/parse would otherwise be a real hot path.
- Tests in `sync/tests/test_drift_check.py`: passthrough, unwrapped methods, falsy-response
  skip, mismatch logs+alerts, new-fields logs+alerts, mismatch+new-fields alerting
  independently on the same method/day, same-day dedup, failed-send retry,
  validation-exception containment, state persistence, webhook no-op/failure-safety.
  `find_new_fields` itself (top-level/nested detection, `$ref` resolution,
  `patternProperties` key exclusion) is tested in `sync/tests/test_schema_validate.py`.
  Enable-flag gating is tested in `sync/tests/test_sync.py` (`_login_and_wrap`), since the
  flag lives in `sync.py`.

`inspect_api.py` remains the tool for deriving a *new* field before it has a schema at all —
`drift_check.py` only covers methods already in `METHOD_SCHEMA`, and only once enabled.

---

## Deferred / open investigations

- **#43** — port `sync/` (Python) to Go. Go *can* do the required JA3/TLS impersonation
  (`utls`, `CycleTLS`) — what's missing is a Garmin-specific client built on top of it
  (SSO/OAuth, MFA, `skip_strategies`-style quirks). Worth it only if maintaining a second
  language purely for this sidecar becomes a real cost, or as a reusable Go library for
  others hitting the same Garmin JA3 wall. See CLAUDE.md for full detail.
- **#42** — verify `strength_training`/rowing `garminconnect` response shape once enough
  real data of that type exists to capture live (same verify-before-build rule as
  everything else — do not guess).
- **weight target (`weight_kg`) on strength workout steps** — deferred from the exercise
  catalog work (#51); `create_workout` doesn't set it yet.
- **step-level `get_workout_detail` MCP tool** — deferred from #51; rollout verification
  currently uses `inspect_api.py get_workout_by_id` manually instead.

---

## Phase 3: Web UI (if warranted) — not started

Go HTTP server (`cmd/web/`) serving:
- Embedded Grafana panel links (iframe or Grafana embedding)
- Chat panel backed by streaming LLM via `internal/llm` provider
- No external frontend framework needed — HTMX + minimal CSS

Decision gate: Phase 2 (CLI) is done. Revisit whether a web UI is actually warranted, or
whether MCP + CLI covers real usage, before starting this.

---

## Config Design (never paint into a corner)

Single `config.yaml` + env var overrides via Viper. Supports:
- Multiple Garmin accounts (map of user → credentials) — future multi-user
- Multiple InfluxDB buckets per user — future multi-user
- Feature flags: `enable_web`, `enable_mcp`, `enable_cli`

---

## Hosting Path

1. **Now**: `docker compose up` / `podman-compose up` on local Mac; MCP server as local binary
2. **Goal**: Deploy to Proxmox (LXC containers) or TrueNAS apps
   - InfluxDB + Grafana + sync sidecar: TrueNAS apps (catalog) or Proxmox Docker VM
   - MCP server: Proxmox LXC or Docker container with HTTP transport (`docker-compose.mcp.yml`)
   - Claude connects to homelab MCP via `http://homelab-ip:8080/mcp` (LAN/Tailscale)
   - `scripts/deploy.sh`, `scripts/setup-lxc.sh`, `deploy/traefik-waypoint.yml` automate this

---

## Public GitHub Repo Baseline

- `README.md`: What it is, prerequisites, quick start, env var reference, work-in-progress note
- `LICENSE`: MIT
- `.env.example`: All env vars documented, no secrets
- `.gitignore`: `.env`, `garmin_tokens.json`, InfluxDB data dirs
- No secrets in code or config defaults

---

## Verification Plan

1. `podman compose up -d` → Grafana at :3001, InfluxDB at :8181 ✓
2. Python sync runs → data appears in InfluxDB ✓
3. Grafana "Garmin Fitness" dashboard shows real data ✓
4. `waypoint-mcp --transport=http` responds to JSON-RPC initialize + tools/list ✓
5. Ask Claude: "How was my training last week?" → calls `get_recent_activities`, returns real data ✓
6. Ask Claude: "What's my readiness today?" → calls `get_training_load` + `get_training_readiness` ✓
7. `waypoint analyze week` returns coaching analysis to terminal via Ollama ✓
8. `waypoint status` shows ATL/CTL/TSB + readiness date ✓
