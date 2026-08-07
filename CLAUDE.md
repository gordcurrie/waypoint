# Waypoint — Claude Code Instructions

## What this is

Personal fitness tracker: Garmin Connect → InfluxDB 3 → Grafana + Go MCP server + Go CLI.
Full architecture in PLAN.md.

## Git workflow (non-negotiable)

**Never commit directly to main.** Every change — no matter how small, in any repo — goes on a feature branch and through a PR. No exceptions for "trivial" fixes, config files, or infra repos.

## Garmin API field verification (non-negotiable)

**Before writing any new sync field, model field, or MCP tool field that reads from
Garmin Connect: verify the actual API response first.** Do not guess field names or
response structure — the Garmin API has non-obvious shapes (nested device-keyed dicts,
integer enums, percent values named "Ratio", missing fields that seem like they should exist).

Bugs burned from skipping this step: `acwRatio` (doesn't exist; real field is
`acwrFactorPercent`), the entire `training_status` response structure (wrong path, wrong
type, wrong field names), `sync_lactate_threshold` reading fields that don't exist at all
(#56), `sync_training_readiness` reading a nonexistent `hrvStatus` key (#58),
`sync_activity_details`'s lap builder reading `avgPower`/`totalAscent` instead of the real
`averagePower`/`elevationGain` (#59), and `sync_performance`/`sync_training_status` both
reading `fitnessAge` from `mostRecentVO2Max.generic` — a field that exists in the response
shape but has always read `null` on this account. Real fitness age lives on a completely
separate, unwrapped endpoint (`fitnessage-service/fitnessage/<date>`, no `garminconnect`
method for it — call `garmin.connectapi()` directly), found 2026-08-02 only because the
Garmin Connect app's own "improved to X" notification didn't match what sync had stored
(nothing, in this case — the field had never once been populated).

Note: an earlier version of this doc claimed "sleep API returns no HRV data at all"
(`avg_hrv_ms` bug). That's now out of date — deriving the schemas below found
`avgOvernightHrv`/`hrvStatus`/`hrvData` present live in `get_sleep_data`. Either Garmin
added these fields since that finding, or the original finding checked the wrong path.
Not currently consumed by any sync field — re-verify before relying on this note either way.

**How to verify:**
1. Check `sync/schemas/*.schema.json` first — one schema per `garminconnect` method
   currently used by `sync.py`, with field names/types/nesting/units/known quirks already
   documented (see `sync/schemas/README.md`). This covers the methods already in use.
2. Run the live capture helper — it also validates the response against that method's
   schema automatically (exits 1 on mismatch), so drift on a covered method surfaces
   immediately instead of needing a manual eyeball diff (#55):
   ```bash
   docker exec waypoint-sync-1 python3 /app/inspect_api.py <method> <date>
   # Examples:
   docker exec waypoint-sync-1 python3 /app/inspect_api.py get_training_readiness 2026-07-24
   docker exec waypoint-sync-1 python3 /app/inspect_api.py get_sleep_data 2026-07-24
   ```
   For a method with no schema yet (new field, nothing to validate against), it just
   prints the raw JSON — still eyeball it by hand before writing code.

   This manual check is no longer the only line of defense: `sync/drift_check.py` wraps
   the Garmin client the live sync loop uses, so every schema-covered method is validated
   during real syncing (#68) — a mismatch logs `ERROR` and alerts via `DRIFT_ALERT_WEBHOOK_URL`
   if set. Off by default (`DRIFT_CHECK_ENABLED=false`) — it's this account's personal drift
   check, not something every clone should run unasked. `inspect_api.py` is still what you
   reach for while deriving a *new* field before it has a schema at all.
3. Identify exact field names, nesting, and types from the real response
4. Include the actual response shape as a comment in the sync function (see `sync_training_status` for the pattern)
5. Do not commit the raw captured response as a fixture — it contains real account PII
   (owner name, profile URLs, GPS lat/long). Encode the shape in a schema instead (see
   `sync/schemas/`), or use a hand-written synthetic payload for tests.

## Non-obvious constraints (don't re-litigate these)

### Python sidecar is required
Garmin sync (`sync/`) must stay Python — not because Go is incapable, but because nobody's
built the Garmin auth layer on Go's equivalent primitives yet.

Cloudflare TLS fingerprinting (JA3) blocks Go's stdlib `net/http` on Garmin SSO as of March
2026 — the ClientHello's cipher/extension order doesn't match a real browser's, so Cloudflare
rejects it before any HTTP logic runs. Python's `curl_cffi` gets past this with real Chrome TLS
impersonation, and `garminconnect` + `curl_cffi` already handle the full SSO/MFA/token flow.

Go *can* do JA3 impersonation — `github.com/refraction-networking/utls` (and `CycleTLS`, which
wraps it) let you construct a custom ClientHello that mimics Chrome exactly. The capability
exists; what's missing is a Garmin-specific client built on top of it (SSO/OAuth, MFA,
`skip_strategies`-style quirks, keeping pace with Chrome's real fingerprint over time). That's
real, ongoing work with no existing library to start from — not a language limitation. See #43
for the investigation into whether that work is worth doing (possible reusable Go library for
others hitting the same Garmin JA3 wall).

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

## Build order (current: Phase 3, not started)

1. ~~Docker Compose — InfluxDB 3 Core + Grafana + sync placeholder~~ ✓ done
2. ~~Python sync sidecar (`sync/`)~~ ✓ done
3. ~~`internal/influx` — InfluxDB client wrapper~~ ✓ done
4. ~~`internal/garmin` — data models~~ ✓ done
5. ~~`internal/analysis` — ATL/CTL/TSB computation~~ ✓ done
6. ~~`tools/` + `cmd/mcp-server/` — MCP server (Phase 1)~~ ✓ done
7. ~~`internal/llm/` + `cmd/cli/` — CLI (Phase 2)~~ ✓ done
8. Web UI (Phase 3) — not started, see PLAN.md ("if warranted")

## Garmin auth constraints (don't re-litigate these)

- **Use `--env-file .env`**, not `-e KEY=value`, when running auth or sync containers. `-e` passes literal strings; placeholder values from examples will cause real login attempts with fake credentials.
- **`skip_strategies` is required** — mobile strategies (`mobile+cffi`, `mobile+requests`) are rate-limited on this account; `widget+cffi` uses `embedWidget=true` which suppresses MFA email delivery. Both `auth.py` and `_garmin_login()` set `garmin.client.skip_strategies = {"mobile+cffi", "mobile+requests", "widget+cffi"}`.
- **Token save is `garmin.client.dump(path)`**, not `garmin.garth.dump(path)`. The `garth` attribute does not exist on the `Garmin` class in garminconnect 0.3.6.
- **Auth volume is `waypoint_sync_data`** — podman compose prefixes the declared `sync_data` volume with the project name (`waypoint`), so the actual Podman volume is `waypoint_sync_data`. Use `-v waypoint_sync_data:/data` in manual `podman run` commands.

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

Approved dependencies (the full list — do not add others without justification):
- `github.com/modelcontextprotocol/go-sdk` — MCP server (no stdlib alternative)
- `github.com/spf13/viper` — config (env + file merging; stdlib env is insufficient)
- `github.com/anthropics/anthropic-sdk-go` — Claude provider (optional, CLI only; official SDK required)

### Dependency policy — be skeptical

Before adding any Go or Python dependency, ask: **can stdlib do this?**

Past example: `influxdb3-go/v2` pulled in 17 transitive deps (arrow, grpc, protobuf,
flatbuffers, lz4, xxh3, …). InfluxDB 3 Core's HTTP API (`/api/v3/query_sql`,
`/api/v3/write_lp`) works fine with `net/http` + `encoding/json`. The SDK was dropped.

Default to stdlib. Add a dep only when:
- The API is genuinely unavailable in stdlib (e.g., TLS fingerprinting, MCP wire protocol), OR
- The dep is an official SDK for a third-party service (Anthropic, etc.), OR
- Implementing it correctly in stdlib would take materially longer than the feature warrants.

When a dep is proposed, name what it replaces and why stdlib falls short.

## Unit conversions in sync.py — backfill required on existing DBs

These fields have non-obvious units from the Garmin API. The field names were fixed in
`feat/sync-sidecar` (July 2026). If applying these fixes to a DB with existing data, the
old wrong-unit points must be deleted and re-synced:

| Field | Measurement | Garmin API unit | Stored unit | Conversion |
|-------|-------------|-----------------|-------------|------------|
| `vertical_oscillation_mm` | `activity` | centimeters | millimeters | ×10 |
| `stride_length_mm` | `activity` | centimeters | millimeters | ×10 |
| `recovery_time_h` | `training_readiness` | minutes | hours | ÷60 |
| `hrv_status` | `training_readiness` | string enum | float | `BALANCED`→2.0, `UNBALANCED`→1.0, `POOR`→0.0 |
| `lt_pace_s_per_km` | `lactate_threshold` | m/s ÷ 10 (raw value is 1/10th of true m/s) | s/km | `100.0 / speed` |

**To backfill**: delete the affected measurements, reset the watermark keys
(`activities`, `training_readiness`, `lactate_threshold`) in `/data/sync_state.json`,
and restart the container. The backfill window is controlled by `BACKFILL_DAYS`
(default 90).

`lt_pace_s_per_km`'s scale factor was resolved 2026-07-30 against connect.garmin.com's
Lactate Threshold report, confirmed at 4 separate dates via the report's own chart
tooltips (exact digit values, not a chart-pixel estimate): raw `speed` only reproduces
the displayed pace via `100.0 / speed` in every case —

| Date | raw `speed` | UI pace | `100.0 / speed` |
|---|---|---|---|
| 2026-01-22 | 0.26944369 | 6:11/km | 371.1s = 6:11.1 |
| 2026-02-04 | 0.29722139 | 5:36/km | 336.5s = 5:36.5 |
| 2026-04-26 | 0.32777686 | 5:05/km | 305.1s = 5:05.1 |
| 2026-07-06 | 0.33888794 | 4:55/km | 295.1s = 4:55.1 |

Bug #56's original fix assumed plain m/s (`1000.0 / speed`) and was still 10x too slow
(2950.8 s/km on the last row) — caught only because the value was implausible on
inspection, not by any automated check. This is the second wrong guess at this field's
unit in the same bug; don't guess a third — verify against the UI report's tooltips
(not just the "Most Recent" tile) if this ever needs re-deriving.

## Garmin workout-service sport/step type IDs (verified — don't re-guess)

`create_workout` uploads write a `sportTypeId`/`sportTypeKey` pair and Garmin's backend
resolves the *ID*, silently overwriting whatever key was sent if the two don't match. A bug
shipped from guessed IDs: `strength_training` was sent as ID 13, which Garmin resolves to
`rucking`; `swimming` was sent as ID 5, which is `strength_training`. Verified 2026-07-28
against this account's `/workout-service/workout/types` plus empirical round-trip tests
(upload a probe workout, read back the sportType Garmin actually assigned, delete it):

| Sport | sportTypeId | Verified how |
|-------|-------------|--------------|
| `running` | 1 | Live in production workouts pre-dating this fix |
| `cycling` | 2 | Empirical round-trip |
| `swimming` | 4 | Empirical round-trip (previously wrongly 5) |
| `strength_training` | 5 | Empirical round-trip (previously wrongly 13 → resolved to "rucking") |
| `walking` | **not supported by Garmin at all** | Confirmed two ways: (1) both candidate IDs (11 → actually `mobility`; 17, a community-library guess) came back nulled on round-trip; (2) Garmin Connect's own workout builder UI (connect.garmin.com/app/workouts → "Select a Workout Type") has no Walking option — only Run, Bike, Pool Swim, Multisport, Strength Training, Cardio, HIIT, Yoga, Pilates, Mobility, Custom. Dropped from `validSports` in `tools/workouts.go`. |

Step types (`_STEP_TYPES` in `sync.py`) were already correct and verified the same way:
`warmup`=1, `cooldown`=2, `interval`=3, `recovery`=4, `steady`→`other`=7. `rest`=5 and
`repeat`=6 were added later (see below) — internal-only, synthesized by
`_build_garmin_repeat_group`, never reachable from a user-supplied step type.

**If you need a sport type not listed here**: do not guess an ID from a public reference
(the `python-garminconnect` library's own typed workout classes got `walking` wrong for
this account). Two verification options, cheapest first:
1. Open connect.garmin.com/app/workouts → create a workout → inspect the "Select a Workout
   Type" dropdown's `<option value="...">` attributes in the page source/devtools — the
   value is the real sportTypeId, no API calls needed.
2. If the type isn't in that dropdown (or you want to confirm anyway), verify empirically:
   upload a disposable probe workout with your candidate ID via `garmin.upload_workout`,
   read it back with `garmin.get_workout_by_id`, confirm the returned `sportTypeKey`
   matches, then delete it with `garmin.delete_workout`.

## Garmin exercise catalog + strength workout structure (verified — don't re-guess)

`create_workout` links a strength step to Garmin's built-in exercise picker via a
`category`/`exerciseName` string pair (e.g. `"BENCH_PRESS"`/`"BARBELL_BENCH_PRESS"`) —
this is what drives the demo animation shown for hand-built workouts. No Garmin API
endpoint exists for this catalog (confirmed 404 on `/workout-service/exercises`,
`/workout-service/exercise/categories`, `/workout-service/workout/exercises`,
`/workout-service/exerciseTypes`). It's a static asset the workout editor's exercise
picker fetches directly:

```
GET https://connect.garmin.com/web-api/web-data/exercises/Exercises.json
```

Direct `curl` gets a 403 — it requires an authenticated browser session (cookies +
same-origin fetch), not a bare request. Capture it from a logged-in browser tab via
`fetch('/web-api/web-data/exercises/Exercises.json', {credentials: 'include'})`; the
response is large (~200KB) so pull it out via a channel that doesn't truncate (a
browser automation tool's direct return value may cap around ~1KB — logging it via
`console.log` and reading it back via a console-log-reading tool worked; dumping into
the page's accessibility tree did not, since that only surfaces short label previews).

Vendored as `internal/garmin/exercises/catalog.json` (1510 exercises, 47 categories as
of 2026-07-28), embedded via stdlib `go:embed` in `internal/garmin/exercises/catalog.go`.
Regenerate with `scripts/generate_exercise_catalog.py` — see that script's docstring for
the exact capture steps. Verified against 8 real `(category, exercise_name)` pairs from
a hand-built strength workout (`get_workout_by_id`) before trusting the capture.

**Do not trust a third-party project's vendored catalog as ground truth** — a reference
project (`cyberjunky/python-garminconnect`, `garminconnect/exercises.py`) exists and is
useful only as a naming-convention sanity check. Exercise availability can differ by
account/region and third-party scrapes go stale; verify against your own account.

**RepeatGroupDTO structure** (how Garmin represents "N sets of X"): confirmed against a
real hand-built workout that `stepOrder` is a single running counter across the *entire*
step tree — the repeat group node itself
consumes one value, then each child consumes the next, with **no reset per group and no
gaps** before the next sibling. `childStepId` marks group membership (shared by the group
node and its children, sequential per group in order of appearance, `null` for ungrouped
steps). `numberOfIterations` and `endConditionValue` on the group must both be set to the
same set count — redundant-looking, but both required.

**`upload_workout`'s response == `get_workout_by_id`'s response** (confirmed 2026-08-07 via
a live probe: uploaded a disposable workout, diffed the `upload_workout` return value against
`get_workout_by_id` on the same id, then `delete_workout`'d it) — same shape, including the
Garmin-assigned `workoutId` and the full `workoutSegments[0].workoutSteps` tree with
server-side `stepId`s. `sync_pending_workouts` uses this to write the `workout_detail`
InfluxDB measurement (read by the `get_workout_detail` MCP tool) straight from the upload
response — no separate `get_workout_by_id` round-trip needed. Each `ExecutableStepDTO` also
carries `weightValue`/`weightUnit` fields — relevant to the still-open `weight_kg` step target
(#86).

## Skill to invoke for MCP server work

When building `tools/` or `cmd/mcp-server/`, invoke the `generate-mcp` skill:
```
/generate-mcp
```
The skill is at `gordcurrie/agent-skills` → `skills/generate-mcp/SKILL.md`.
Follow its conventions for client interface, helpers, registration pattern, and transport.

## Hosting

Local dev: `podman compose up`, MCP server as local binary (`stdio` transport).
Homelab goal: InfluxDB + Grafana + sync on Proxmox/TrueNAS, MCP server as Docker container
with `--transport=http`, Claude connects to `http://homelab-ip:8080/mcp`.
