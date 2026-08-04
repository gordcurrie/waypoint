# Garmin API schemas

JSON Schema (draft 2020-12) for every `garminconnect` method currently called from
`sync/sync.py`. Part of #49/#54.

## Why JSON Schema, not raw fixtures or typed Python models

An earlier version of this work committed raw captured JSON payloads instead. Code
review flagged that those fixtures carried real personal data (owner name, profile
image URLs, precise GPS lat/long) — safe structurally, not safe to commit verbatim.
JSON Schema documents field names, nesting, types, and enums without ever containing
real values, so it captures everything needed to develop against without the privacy
problem.

JSON Schema was also chosen over Python-only typed models (TypedDict etc.) because
the sync sidecar's data-shape layer may eventually move to another language — a
schema format is checkable from any language, not just Python.

## Methodology

Each schema was derived from real Garmin Connect responses, captured live via
`inspect_api.py` and then discarded once their structure was encoded here. Dates and
edge cases used are noted in each schema's top-level `description`. In summary:

| Method | Schema file | Edge cases covered |
|---|---|---|
| `get_activities_by_date` | `activities.schema.json` | 15-day range, running/track_running only |
| `get_stats` | `daily_stats.schema.json` | activity day + rest day |
| `get_sleep_data` | `sleep.schema.json` | two full nights (no missing-sleep sample yet) |
| `get_hrv_data` | `hrv.schema.json` | BALANCED and UNBALANCED status values |
| `get_training_readiness` | `training_readiness.schema.json` | activity day + rest day |
| `get_training_status` | `training_status.schema.json` | device-keyed nesting confirmed |
| `get_max_metrics` | `performance.schema.json` | non-empty list + empty-list (no update that day) |
| `get_lactate_threshold` | `lactate_threshold.schema.json` | most-recent test only |
| `get_activity_splits` | `activity_splits.schema.json` | one real running activity |
| `get_activity_hr_in_timezones` | `activity_hr_zones.schema.json` | one real running activity |
| `get_scheduled_workouts` | `scheduled_workouts.schema.json` | two months, zero real scheduled workouts in either |
| `get_respiration_data` | `respiration.schema.json` | two days |
| `get_adaptive_training_plan_by_id` | `adaptive_training_plan.schema.json` | one active FBT_ADAPTIVE plan; rest/base/tempo/long-run task mix |
| `get_training_plans` | `training_plans.schema.json` | one active plan |

`vo2max.schema.json` is a shared `$defs` file, not tied to one method — `get_max_metrics`'s
`generic`/`heatAltitudeAcclimation` objects and `get_training_status`'s
`mostRecentVO2Max.generic`/`heatAltitudeAcclimation` are the same underlying Garmin
object returned by two different endpoints; `performance.schema.json` and
`training_status.schema.json` both `$ref` into it so the two can't drift independently.

`fitness_age.schema.json` documents `GET fitnessage-service/fitnessage/<date>` —
not a `garminconnect`-wrapped method, called directly via `garmin.connectapi(...)`
(found 2026-08-02; see CLAUDE.md's Garmin API field verification bug list). Not
in `schema_validate.METHOD_SCHEMA` — the path includes a date, so it doesn't fit
the exact-method-name mapping `inspect_api.py` uses; validate it by hand against
a raw `connectapi()` capture if this endpoint needs re-deriving.

`workout_upload.schema.json` documents the outbound `upload_workout` request body
built by `sync.py`'s `_build_garmin_workout`/`_build_garmin_step`/`_build_garmin_repeat_group`
— not derived from a live capture like the others (`upload_workout` is a write endpoint,
out of scope for the read-method sweep above). It's the structural record for what used
to be verified against `sync/tests/fixtures/workout_1646566436.json` before that file was
removed for containing real account PII — see CLAUDE.md's "RepeatGroupDTO structure" note.
Validated against `_build_garmin_workout`'s actual output (a warmup + 3-set bench-press
repeat group + cooldown) with zero schema violations.

## Known gaps (not covered — see per-field notes in the schemas themselves)

- Sleep data on a night with no recording at all
- HRV on a day with no reading (device not worn)
- A scheduled-workout calendar item with `workoutId` actually set
- `get_lactate_threshold` for an account with no LT test ever taken
- Non-running activity types (cycling, swimming, strength_training)

## Bugs found while deriving these schemas

Comparing `sync.py`'s field reads against the real shapes surfaced four live bugs.
All four are now fixed:

- **#56** (fixed, twice) — `sync_lactate_threshold` read `heartRateThreshold`/`paceThreshold`/`testDate`,
  none of which exist. Real data is nested under `speed_and_heart_rate`. The first fix
  assumed `speed` was plain m/s (`1000.0 / speed`) — still wrong, 10x off. The raw value
  is actually 1/10th of true m/s; verified against connect.garmin.com's own Lactate
  Threshold report's chart tooltips at 4 separate dates (exact digit values). Correct
  conversion is `100.0 / speed`. See CLAUDE.md's unit conversion table.
- **#58** (fixed) — `sync_training_readiness` read a nonexistent `hrvStatus` key. The real
  HRV-related fields here (`hrvFactorPercent`/`hrvFactorFeedback`) are a different
  signal than the `BALANCED`/`UNBALANCED`/`POOR` enum, which actually lives in
  `get_hrv_data` (already synced correctly, separately) — fix dropped the broken
  `hrv_status` field from `training_readiness` entirely rather than mapping the
  differently-shaped real fields.
- **#59** (fixed) — `sync_activity_details`'s lap builder read `avgPower`/`totalAscent`;
  real keys are `averagePower`/`elevationGain`.
- **#62** (fixed) — `sync_scheduled_workouts` read `item.get("sport") or item.get("activityType")`,
  neither of which exist. Real key is the flat string `sportTypeKey`.

## Validation wired up (#55)

`inspect_api.py` validates its live output against the matching schema (see
`schema_validate.METHOD_SCHEMA` for the method → schema-file mapping) and exits
1 on a mismatch, printing each violation's path and message. A method with no
entry in that mapping just prints a note that nothing was checked — not a
failure; it means no schema has been derived for that method yet.

## Automatic drift detection (#68)

The above only covers the interactive dev workflow (someone running `inspect_api.py`
while deriving a new field). `drift_check.py` closes the other gap — it wraps the
`Garmin` client the live sync loop uses so every schema-covered call in `SYNC_FUNCS`
is validated, on the responses already being fetched for real syncing (no separate
polling job, no extra Garmin API traffic or rate-limit/auth risk). A mismatch logs
`ERROR` and alerts via `DRIFT_ALERT_WEBHOOK_URL` if set (unset = log-only); it never
raises, so a stale schema can't break real syncing. Falsy responses (`None`/`{}`/`[]`)
are skipped — every `sync_*` call site already treats those as "no data today," not
a schema violation.

Off by default (`DRIFT_CHECK_ENABLED=false`) — this validates one specific Garmin
account's data and posts to one specific webhook, not behavior every clone of this
repo should get unasked.
