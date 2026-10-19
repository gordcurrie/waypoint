"""
Garmin Connect → InfluxDB 3 sync sidecar.

Data synced: activities, daily_stats, sleep, hrv, training_readiness,
             training_status, performance (VO2 max), lactate_threshold,
             respiration.

First run: backfills BACKFILL_DAYS (default 90) of history.
Subsequent runs: incremental from last-synced date per measurement.
State persisted in DATA_DIR/sync_state.json.

2FA note: if your Garmin account requires 2FA, do a one-time interactive
login outside the container to seed TOKEN_STORE, then restart.
"""

import errno
import json
import logging
import os
import re
import time
from collections.abc import Callable
from datetime import UTC, date, datetime, timedelta, timezone
from pathlib import Path
from typing import Any

from garminconnect import (
    Garmin,
    GarminConnectAuthenticationError,
    GarminConnectConnectionError,
    GarminConnectTooManyRequestsError,
)
from influxdb_client_3 import InfluxDBClient3, Point

import drift_check

# ── Config ─────────────────────────────────────────────────────────────────────

GARMIN_EMAIL = os.environ["GARMIN_EMAIL"]
GARMIN_PASSWORD = os.environ["GARMIN_PASSWORD"]
GARMIN_MFA_CODE = os.environ.get("GARMIN_MFA_CODE", "")  # one-shot MFA for automated envs
INFLUXDB_URL = os.environ["INFLUXDB_URL"]
INFLUXDB_DB = os.environ.get("INFLUXDB_DATABASE", "garmin")
INFLUXDB_TOKEN = os.environ.get("INFLUXDB_TOKEN", "")
BACKFILL_DAYS = int(os.environ.get("BACKFILL_DAYS", "90"))
# Off by default — hits a personal Garmin account + personal alert webhook, not
# something every clone of this repo should do without opting in (#68).
DRIFT_CHECK_ENABLED = os.environ.get("DRIFT_CHECK_ENABLED", "false").strip().lower() in (
    "1",
    "true",
    "yes",
)
DATA_DIR = Path(os.environ.get("DATA_DIR", "/data"))
TOKEN_STORE = str(DATA_DIR / "garmin_auth")
STATE_FILE = DATA_DIR / "sync_state.json"

# Guard against SYNC_SCHEDULE="" which would cause IndexError on split()[0]
_cron = os.environ.get("SYNC_SCHEDULE", "*/30 * * * *")
_cron_parts = _cron.split()
_interval_match = re.match(r"\*/(\d+)", _cron_parts[0]) if _cron_parts else None
SYNC_INTERVAL_S = int(_interval_match.group(1)) * 60 if _interval_match else 1800
_sync_schedule_warning = (
    None
    if _interval_match
    else f"SYNC_SCHEDULE={_cron!r} not in */N minute format — defaulting to {SYNC_INTERVAL_S}s interval"
)

log = logging.getLogger(__name__)


# ── State ──────────────────────────────────────────────────────────────────────


def _load_state() -> dict[str, Any]:
    if STATE_FILE.exists():
        try:
            return json.loads(STATE_FILE.read_text())
        except (json.JSONDecodeError, OSError):
            log.warning("State file corrupt — resetting to empty state")
            return {}
    return {}


def _save_state(state: dict[str, Any]) -> None:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    tmp = STATE_FILE.with_suffix(".tmp")
    tmp.write_text(json.dumps(state, indent=2))
    tmp.replace(STATE_FILE)


def _last_synced(state: dict[str, Any], key: str) -> date:
    if key in state:
        try:
            return date.fromisoformat(state[key])
        except ValueError:
            log.warning(
                "State key %r has invalid date %r — resetting to backfill start", key, state[key]
            )
    return date.today() - timedelta(days=BACKFILL_DAYS)


# ── Auth ───────────────────────────────────────────────────────────────────────


def _login_and_wrap() -> Any:
    garmin = _garmin_login()
    return drift_check.wrap(garmin) if DRIFT_CHECK_ENABLED else garmin


def _garmin_login() -> Garmin:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    try:
        garmin = Garmin()
        # Must set before login — matches credential fallback below.
        garmin.client.skip_strategies = {"mobile+cffi", "mobile+requests", "widget+cffi"}
        garmin.login(TOKEN_STORE)
        log.info("Logged in via saved token")
        return garmin
    except Exception as exc:
        # Covers FileNotFoundError, corrupt JSON, expired token, garth network errors
        log.debug("Token login failed (%s) — falling back to credentials", exc)
        pass

    log.info("No valid token — authenticating with credentials")
    mfa_cb = (lambda: GARMIN_MFA_CODE) if GARMIN_MFA_CODE else None
    garmin = Garmin(email=GARMIN_EMAIL, password=GARMIN_PASSWORD, prompt_mfa=mfa_cb)
    # Skip mobile strategies (rate-limited) and widget+cffi (suppresses MFA email).
    # Mirrors auth.py — portal+cffi uses clientId=GarminConnect and triggers email MFA.
    garmin.client.skip_strategies = {"mobile+cffi", "mobile+requests", "widget+cffi"}
    garmin.login()
    try:
        garmin.client.dump(TOKEN_STORE)
        log.info("Saved auth token to %s", TOKEN_STORE)
    except Exception as exc:
        log.warning(
            "Failed to save auth token to %s: %s — will re-authenticate next restart",
            TOKEN_STORE,
            exc,
        )
    return garmin


# ── InfluxDB ───────────────────────────────────────────────────────────────────


def _influx_client() -> InfluxDBClient3:
    return InfluxDBClient3(
        host=INFLUXDB_URL,
        database=INFLUXDB_DB,
        token=INFLUXDB_TOKEN,
    )


def _write(client: InfluxDBClient3, points: list[Any]) -> None:
    if points:
        client.write(record=points)


# ── Helpers ────────────────────────────────────────────────────────────────────


def _parse_gmt(ts: str) -> datetime:
    # Take first 19 chars (YYYY-MM-DDTHH:MM:SS) to strip fractional seconds and TZ offsets
    # (Garmin returns both "...T10:30:00.000" and "...T10:30:00+00:00" forms).
    return datetime.strptime(ts[:19].replace("T", " "), "%Y-%m-%d %H:%M:%S").replace(tzinfo=UTC)


def _day_ts(d: date) -> datetime:
    return datetime(d.year, d.month, d.day, tzinfo=UTC)


def _fval(data: Any, *keys: str) -> float | None:
    """Safe nested float extraction from a dict."""
    v = data
    for k in keys:
        if not isinstance(v, dict):
            return None
        v = v.get(k)
    if v is None:
        return None
    try:
        return float(v)
    except (TypeError, ValueError):
        return None


def _scale(v: float | None, factor: float) -> float | None:
    """Multiply v by factor; propagate None."""
    return v * factor if v is not None else None


def _add_fields(p: Point, fields: dict[str, Any]) -> tuple[Point, int]:
    """Add non-None fields to point; return (point, count_added)."""
    count = 0
    for k, v in fields.items():
        if v is not None:
            p = p.field(k, v)
            count += 1
    return p, count


def _advance_state(state: dict[str, Any], key: str, end: date, *, had_error: bool = False) -> None:
    """Advance watermark to end unless it would regress."""
    existing_str = state.get(key)
    if existing_str:
        try:
            if date.fromisoformat(existing_str) >= end:
                return  # regression guard: never move watermark backward
        except ValueError:
            log.warning("State key %r has invalid date %r — overwriting", key, existing_str)
    state[key] = end.isoformat()
    _save_state(state)
    if had_error:
        log.warning(
            "%s: synced with errors — watermark at %s (pre-error days will retry)", key, end
        )


# ── Sync functions ─────────────────────────────────────────────────────────────


def sync_activities(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    start = _last_synced(state, "activities")
    end = date.today()
    log.info("activities: %s → %s", start, end)

    activities = garmin.get_activities_by_date(start.isoformat(), end.isoformat()) or []

    points = []
    _had_error = False
    for a in activities:
        try:
            ts_str = a.get("startTimeGMT")
            if not ts_str:
                continue

            sport = (a.get("activityType") or {}).get("typeKey", "unknown")
            p = Point("activity").tag("sport", sport).time(_parse_gmt(ts_str))

            # Use is not None check to handle activityId=0 correctly
            fields: dict[str, Any] = {
                "activity_id": int(a["activityId"]) if a.get("activityId") is not None else None,
                "distance_m": _fval(a, "distance"),
                "duration_s": _fval(a, "duration"),
                "avg_hr_bpm": _fval(a, "averageHR"),
                "max_hr_bpm": _fval(a, "maxHR"),
                "calories_kcal": _fval(a, "calories"),
                "elevation_gain_m": _fval(a, "elevationGain"),
                "avg_speed_m_s": _fval(a, "averageSpeed"),
                "training_load": _fval(a, "activityTrainingLoad"),
                "aerobic_te": _fval(a, "aerobicTrainingEffect"),
                "anaerobic_te": _fval(a, "anaerobicTrainingEffect"),
                "vo2max": _fval(a, "vO2MaxValue"),
            }
            if sport in ("running", "trail_running", "treadmill_running", "indoor_running"):
                fields.update(
                    {
                        "cadence_avg_spm": _fval(a, "averageRunningCadenceInStepsPerMinute"),
                        "ground_contact_time_ms": _fval(a, "avgGroundContactTime"),
                        "vertical_oscillation_mm": _scale(_fval(a, "avgVerticalOscillation"), 10.0),
                        "stride_length_mm": _scale(_fval(a, "avgStrideLength"), 10.0),
                        "vertical_ratio_pct": _fval(a, "avgVerticalRatio"),
                        "avg_power_w": _fval(a, "avgPower"),
                    }
                )

            p, n = _add_fields(p, fields)
            if n:
                points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            _had_error = True
            log.warning("activity %s: %s", a.get("activityId"), exc)

    _write(client, points)
    _advance_state(state, "activities", end, had_error=_had_error)
    log.info("activities: wrote %d points", len(points))


def sync_daily_stats(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    start = _last_synced(state, "daily_stats")
    end = date.today()
    log.info("daily_stats: %s → %s", start, end)

    points = []
    d = start
    _first_err: date | None = None
    while d <= end:
        try:
            s = garmin.get_stats(d.isoformat())
            if s:
                p = Point("daily_stats").time(_day_ts(d))
                fields = {
                    "steps": _fval(s, "totalSteps"),
                    "resting_hr_bpm": _fval(s, "restingHeartRate"),
                    "body_battery_max": _fval(s, "bodyBatteryHighestValue"),
                    "body_battery_min": _fval(s, "bodyBatteryLowestValue"),
                    "stress_avg": _fval(s, "averageStressLevel"),
                    "active_calories": _fval(s, "activeKilocalories"),
                    "total_calories": _fval(s, "totalKilocalories"),
                    "floors_ascended": _fval(s, "floorsAscended"),
                    "vigorous_intensity_min": _fval(s, "vigorousIntensityMinutes"),
                    "moderate_intensity_min": _fval(s, "moderateIntensityMinutes"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("daily_stats %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = max((_first_err - timedelta(days=1)) if _first_err else end, start)
    _write(client, points)
    _advance_state(state, "daily_stats", watermark, had_error=_first_err is not None)
    log.info("daily_stats: wrote %d points", len(points))


def sync_sleep(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    start = _last_synced(state, "sleep")
    end = date.today()
    log.info("sleep: %s → %s", start, end)

    points = []
    d = start
    _first_err: date | None = None
    while d <= end:
        try:
            raw = garmin.get_sleep_data(d.isoformat())
            if raw:
                daily = raw.get("dailySleepDTO") or {}
                # sleepScores lives inside dailySleepDTO, not at the top level
                _ss = daily.get("sleepScores")
                scores: dict[str, Any] = _ss or {}
                p = Point("sleep").time(_day_ts(d))
                fields = {
                    "total_sleep_s": _fval(daily, "sleepTimeSeconds"),
                    "deep_sleep_s": _fval(daily, "deepSleepSeconds"),
                    "light_sleep_s": _fval(daily, "lightSleepSeconds"),
                    "rem_sleep_s": _fval(daily, "remSleepSeconds"),
                    "awake_s": _fval(daily, "awakeSleepSeconds"),
                    "sleep_score": _fval(scores, "overall", "value"),
                    "avg_spo2_pct": _fval(daily, "averageSpO2Value"),
                    "avg_breathing_rate": _fval(daily, "averageRespirationValue"),
                    "avg_stress": _fval(daily, "avgSleepStress"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("sleep %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = max((_first_err - timedelta(days=1)) if _first_err else end, start)
    _write(client, points)
    _advance_state(state, "sleep", watermark, had_error=_first_err is not None)
    log.info("sleep: wrote %d points", len(points))


def sync_hrv(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    start = _last_synced(state, "hrv")
    end = date.today()
    log.info("hrv: %s → %s", start, end)

    status_num = {"BALANCED": 2.0, "UNBALANCED": 1.0, "POOR": 0.0}
    points = []
    d = start
    _first_err: date | None = None
    while d <= end:
        try:
            raw = garmin.get_hrv_data(d.isoformat())
            if raw:
                # get_hrv_data can return a list or a dict; normalise to dict first
                item = raw[0] if isinstance(raw, list) else raw
                summary = (item.get("hrvSummary") or item) if isinstance(item, dict) else item
                p = Point("hrv").time(_day_ts(d))
                fields = {
                    "weekly_avg_ms": _fval(summary, "weeklyAvg"),
                    "last_night_ms": _fval(summary, "lastNightAvg"),
                    "last_5min_high_ms": _fval(summary, "lastNight5MinHigh"),
                    "status": status_num.get(str(summary.get("status", "")), None),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("hrv %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = max((_first_err - timedelta(days=1)) if _first_err else end, start)
    _write(client, points)
    _advance_state(state, "hrv", watermark, had_error=_first_err is not None)
    log.info("hrv: wrote %d points", len(points))


def sync_training_readiness(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    start = _last_synced(state, "training_readiness")
    end = date.today()
    log.info("training_readiness: %s → %s", start, end)

    points = []
    d = start
    _first_err: date | None = None
    while d <= end:
        try:
            raw = garmin.get_training_readiness(d.isoformat())
            if raw:
                item = raw[0] if isinstance(raw, list) else raw
                p = Point("training_readiness").time(_day_ts(d))
                fields = {
                    "score": _fval(item, "score"),
                    "sleep_score": _fval(item, "sleepScore"),
                    "recovery_time_h": _scale(_fval(item, "recoveryTime"), 1.0 / 60.0),
                    "acw_pct": _fval(item, "acwrFactorPercent"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("training_readiness %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = max((_first_err - timedelta(days=1)) if _first_err else end, start)
    _write(client, points)
    _advance_state(state, "training_readiness", watermark, had_error=_first_err is not None)
    log.info("training_readiness: wrote %d points", len(points))


def sync_training_status(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    start = _last_synced(state, "training_status")
    end = date.today()
    log.info("training_status: %s → %s", start, end)

    status_num = {
        "peaking": 5.0,
        "maintaining": 4.0,
        "productive": 3.0,
        "recovery": 2.0,
        "detraining": 1.0,
        "overreaching": 0.0,
    }
    points = []
    d = start
    _first_err: date | None = None
    while d <= end:
        try:
            raw = garmin.get_training_status(d.isoformat())
            if raw and isinstance(raw, dict):
                # API shape: {mostRecentTrainingStatus: {latestTrainingStatusData: {<deviceId>: {...}}}}
                # latestTrainingStatusData is keyed by device ID, not a flat dict.
                # trainingStatus is an integer; trainingStatusFeedbackPhrase ("PRODUCTIVE_2") is the
                # reliable string — it starts with the status name we want to map.
                ts_by_device: dict[str, Any] = (raw.get("mostRecentTrainingStatus") or {}).get(
                    "latestTrainingStatusData"
                ) or {}
                device_entry: dict[str, Any] | None = next(
                    (
                        v
                        for v in ts_by_device.values()
                        if isinstance(v, dict) and v.get("primaryTrainingDevice")
                    ),
                    next((v for v in ts_by_device.values() if isinstance(v, dict)), None),
                )
                if device_entry:
                    phrase = str(device_entry.get("trainingStatusFeedbackPhrase") or "").lower()
                    status_val: float | None = next(
                        (v for k, v in status_num.items() if phrase.startswith(k)),
                        None,
                    )
                    p = Point("training_status").time(_day_ts(d))
                    fields: dict[str, Any] = {
                        "status_num": status_val,
                        "vo2max_running": _fval(
                            raw, "mostRecentVO2Max", "generic", "vo2MaxPreciseValue"
                        ),
                        "vo2max_cycling": _fval(
                            raw, "mostRecentVO2Max", "cycling", "vo2MaxPreciseValue"
                        ),
                    }
                    p, n = _add_fields(p, fields)
                    if n:
                        points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("training_status %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = max((_first_err - timedelta(days=1)) if _first_err else end, start)
    _write(client, points)
    _advance_state(state, "training_status", watermark, had_error=_first_err is not None)
    log.info("training_status: wrote %d points", len(points))


def sync_performance(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    """VO2 max / fitness age per day. Lactate threshold written to its own measurement.

    VO2 max API shape (verified 2026-07-26 via get_max_metrics):
    [{"userId": ..., "generic": {"calendarDate": "2025-01-08",
      "vo2MaxPreciseValue": 39.4, "vo2MaxValue": 39.0, "fitnessAge": 47,
      "fitnessAgeDescription": 0, "maxMetCategory": 0},
      "cycling": null, "heatAltitudeAcclimation": null}]
    A list, despite the garminconnect type hint claiming dict. Empty list
    on days with no VO2max update (not an error).

    Bug (found 2026-08-02): generic.fitnessAge above always reads null on this
    account — confirmed live, not a formatting quirk. Real fitness age comes
    from a completely separate, unwrapped endpoint (no garminconnect method
    exists for it): GET fitnessage-service/fitnessage/<date>, e.g.
    {"chronologicalAge": 50, "fitnessAge": 44.74, "achievableFitnessAge": 43.4,
     "previousFitnessAge": 44.01, "components": {...}, "lastUpdated": ...}.
    On days with insufficient rolling data ("stale" components), the
    top-level "fitnessAge" key is absent entirely, not null — use .get().
    Verified against the Garmin Connect UI's own Fitness Age page (matched
    within rounding of the in-app "improved to X" notification).
    """
    start = _last_synced(state, "performance")
    end = date.today()
    log.info("performance: %s → %s", start, end)

    points = []
    d = start
    _first_err: date | None = None
    while d <= end:
        try:
            raw = garmin.get_max_metrics(d.isoformat())
            try:
                fitness_age_raw = garmin.connectapi(
                    f"fitnessage-service/fitnessage/{d.isoformat()}"
                )
            except (
                GarminConnectAuthenticationError,
                GarminConnectTooManyRequestsError,
                GarminConnectConnectionError,
            ):
                raise
            except Exception as exc:
                # Isolated from the outer try: a fitness-age-specific failure must not
                # also drop that day's vo2max (already fetched above) or stall the
                # watermark on a metric that isn't even the one that failed.
                fitness_age_raw = None
                log.warning("performance %s: fitness_age fetch failed: %s", d, exc)
            if raw or fitness_age_raw:
                item = (raw[0] if isinstance(raw, list) else raw) if raw else {}
                generic = item.get("generic") or item
                p = Point("performance").time(_day_ts(d))
                fields = {
                    "vo2max": _fval(generic, "vo2MaxPreciseValue"),
                    "fitness_age": _fval(fitness_age_raw or {}, "fitnessAge"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("performance %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = max((_first_err - timedelta(days=1)) if _first_err else end, start)
    _write(client, points)
    _advance_state(state, "performance", watermark, had_error=_first_err is not None)
    log.info("performance: wrote %d points", len(points))


def sync_lactate_threshold(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    """Most-recent lactate threshold result — separate measurement avoids timestamp collision with performance.

    API shape (verified 2026-07-30 against Garmin Connect UI, see
    sync/schemas/lactate_threshold.schema.json):
    {"speed_and_heart_rate": {"calendarDate": ..., "speed": <1/10th of true m/s>, "heartRate": <bpm>, ...},
     "power": {...}}
    HR/pace/date all live under speed_and_heart_rate — there is no top-level
    heartRateThreshold/paceThreshold/testDate. speed is 1/10th of true m/s (not
    plain m/s, and not s/m) — confirmed against connect.garmin.com's Lactate
    Threshold report's chart tooltips at 4 separate dates (exact digit values,
    see CLAUDE.md's unit table): raw speed only matches the displayed pace via
    100.0 / speed in every case, e.g. 0.33888794 -> 4:55/km (295 s/km). Bug #56's
    first fix (1000.0 / speed) assumed plain m/s and was still off by 10x
    (2950.8 s/km).
    """
    log.info("lactate_threshold: fetching most recent")
    try:
        lt = garmin.get_lactate_threshold()
        if not lt:
            return
        shr = lt.get("speed_and_heart_rate") or {}
        # Use the actual test date from the API response; fall back to today
        raw_date = shr.get("calendarDate")
        if raw_date:
            try:
                test_date = date.fromisoformat(str(raw_date)[:10])
            except (ValueError, TypeError):
                test_date = date.today()
        else:
            test_date = date.today()

        # Skip re-write if we already have this test date
        if state.get("lactate_threshold") == test_date.isoformat():
            log.info("lactate_threshold: already synced %s", test_date)
            return

        p = Point("lactate_threshold").time(_day_ts(test_date))
        speed_raw = _fval(shr, "speed")
        fields = {
            "lt_hr_bpm": _fval(shr, "heartRate"),
            "lt_pace_s_per_km": (100.0 / speed_raw) if speed_raw else None,
        }
        p, n = _add_fields(p, fields)
        if n:
            _write(client, [p])
            state["lactate_threshold"] = test_date.isoformat()
            _save_state(state)
            log.info("lactate_threshold: wrote 1 point at %s", test_date)
    except (
        GarminConnectAuthenticationError,
        GarminConnectTooManyRequestsError,
        GarminConnectConnectionError,
    ):
        raise
    except Exception as exc:
        log.warning("lactate_threshold: %s", exc)


def sync_activity_details(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    """Fetch per-lap splits, HR zone distribution, and (strength_training only) exercise
    sets for each recent activity."""
    start = _last_synced(state, "activity_details")
    end = date.today()
    log.info("activity_details: %s → %s", start, end)

    activities = garmin.get_activities_by_date(start.isoformat(), end.isoformat()) or []

    points: list[Any] = []
    _first_err: date | None = None
    for a in activities:
        activity_id = a.get("activityId")
        if activity_id is None:
            continue
        ts_str = a.get("startTimeGMT")
        if not ts_str:
            continue
        try:
            activity_ts = _parse_gmt(ts_str)
        except Exception:
            continue
        activity_date = activity_ts.date()

        # Per-lap splits
        try:
            splits = garmin.get_activity_splits(str(activity_id)) or {}
            for lap in splits.get("lapDTOs") or []:
                lap_idx = lap.get("lapIndex") or 0
                lap_ts_str = lap.get("startTimeGMT")
                try:
                    lap_ts = _parse_gmt(lap_ts_str) if lap_ts_str else activity_ts
                except Exception:
                    lap_ts = activity_ts
                p = Point("activity_lap").tag("activity_id", str(activity_id)).time(lap_ts)
                fields: dict[str, Any] = {
                    "lap_index": float(lap_idx),
                    "distance_m": _fval(lap, "distance"),
                    "duration_s": _fval(lap, "duration"),
                    "avg_hr_bpm": _fval(lap, "averageHR"),
                    "max_hr_bpm": _fval(lap, "maxHR"),
                    "avg_speed_m_s": _fval(lap, "averageSpeed"),
                    "avg_cadence_spm": _fval(lap, "averageRunCadence"),
                    "avg_power_w": _fval(lap, "averagePower"),
                    "elevation_gain_m": _fval(lap, "elevationGain"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = activity_date
            log.warning("activity_splits %s: %s", activity_id, exc)

        time.sleep(0.3)

        # HR zone distribution
        try:
            zones_raw = garmin.get_activity_hr_in_timezones(str(activity_id)) or []
            zones: list[Any] = (
                zones_raw if isinstance(zones_raw, list) else zones_raw.get("hrTimeInZones") or []
            )
            zone_secs: dict[int, float] = {}
            for z in zones:
                znum = z.get("zoneNumber")
                secs = _fval(z, "secsInZone")
                if znum is not None and secs is not None:
                    zone_secs[int(znum)] = secs
            if zone_secs:
                p = (
                    Point("activity_hr_zones")
                    .tag("activity_id", str(activity_id))
                    .time(activity_ts)
                )
                hr_fields: dict[str, Any] = {
                    "z1_s": zone_secs.get(1),
                    "z2_s": zone_secs.get(2),
                    "z3_s": zone_secs.get(3),
                    "z4_s": zone_secs.get(4),
                    "z5_s": zone_secs.get(5),
                }
                p, n = _add_fields(p, hr_fields)
                if n:
                    points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = activity_date
            log.warning("activity_hr_zones %s: %s", activity_id, exc)

        time.sleep(0.3)

        # Exercise sets (strength_training only — #42; not present in the summary
        # response at all, needs this separate call, and only meaningful for this
        # activity type).
        #
        # API shape (verified 2026-08-09 against a real 43-set activity, see
        # sync/schemas/activity_exercise_sets.schema.json):
        # {"activityId": ..., "exerciseSets": [
        #   {"exercises": [{"category": "LUNGE", "name": "LUNGE", "probability": 99.6}, ...],
        #    "duration": 40.0, "repetitionCount": 6 | null, "weight": null,
        #    "setType": "ACTIVE" | "REST", "startTime": "<GMT, matches startTimeGMT format>",
        #    "wktStepIndex": ..., "messageIndex": ...}, ...]}
        # exercises is a ranked list of ML candidates (top one used here) — every captured
        # set had all candidates identical at ~99.6% probability, i.e. never actually
        # ambiguous in this account's sample. repetitionCount is null on REST, sometimes a
        # real 0 on ACTIVE (device detected the exercise but didn't count reps). weight has
        # always been null — no device in this account ever logged one.
        if (a.get("activityType") or {}).get("typeKey") == "strength_training":
            try:
                detail = garmin.get_activity_exercise_sets(str(activity_id)) or {}
                for exercise_set in detail.get("exerciseSets") or []:
                    set_ts_str = exercise_set.get("startTime")
                    try:
                        set_ts = _parse_gmt(set_ts_str) if set_ts_str else activity_ts
                    except Exception:
                        set_ts = activity_ts
                    msg_idx = exercise_set.get("messageIndex")
                    p = (
                        Point("activity_exercise_set")
                        .tag("activity_id", str(activity_id))
                        .tag("set_index", str(msg_idx if msg_idx is not None else 0))
                        .time(set_ts)
                    )
                    exercises = exercise_set.get("exercises") or []
                    top = exercises[0] if exercises else {}
                    exercise_fields: dict[str, Any] = {
                        "category": top.get("category"),
                        "exercise_name": top.get("name"),
                        "duration_s": _fval(exercise_set, "duration"),
                        "reps": _fval(exercise_set, "repetitionCount"),
                        "weight_kg": _fval(exercise_set, "weight"),
                        "set_type": exercise_set.get("setType"),
                    }
                    p, n = _add_fields(p, exercise_fields)
                    if n:
                        points.append(p)
            except (
                GarminConnectAuthenticationError,
                GarminConnectTooManyRequestsError,
                GarminConnectConnectionError,
            ):
                raise
            except Exception as exc:
                if _first_err is None:
                    _first_err = activity_date
                log.warning("activity_exercise_sets %s: %s", activity_id, exc)

            time.sleep(0.3)

    watermark = max((_first_err - timedelta(days=1)) if _first_err else end, start)
    _write(client, points)
    _advance_state(state, "activity_details", watermark, had_error=_first_err is not None)
    log.info("activity_details: wrote %d points for %d activities", len(points), len(activities))


def _query_active_keys(
    client: InfluxDBClient3,
    start: date,
    end: date,
    tags: tuple[str, ...],
    missing_column_expected: bool = False,
) -> set[tuple[str, ...]]:
    """Return the (date, *tags) keys currently stored for scheduled_workout points in
    [start, end) whose first tag is set. Used by sync_scheduled_workouts to find stale
    entries to tombstone — coach-plan entries keyed by ("workout_name", "sport") (#104), and
    self-created ones by ("scheduled_id", "sport"). Deliberately does not filter on
    deleted_at, so an already-tombstoned key is returned here too and gets harmlessly
    re-tombstoned on every run until it ages out of the sync window entirely.

    A tag missing from a row reads back as None and is mapped to "": influxdb_client_3 drops
    an empty-string tag on write, so a key built from a fresh item with an empty sport ("")
    and the stored row (no sport tag) must compare equal — and a tombstone written with ""
    is likewise dropped, landing on that same tagless series.
    """
    # Explicit UTC-midnight RFC3339 timestamps, not date-only literals (#105 review) —
    # matches how every other bounded time query in this codebase (e.g.
    # tools/workouts.go's queryMeasurementRange) formats time bounds, and avoids
    # relying on an implicit date-string cast in InfluxDB's SQL engine.
    start_ts = _day_ts(start).strftime("%Y-%m-%dT%H:%M:%SZ")
    end_ts = _day_ts(end).strftime("%Y-%m-%dT%H:%M:%SZ")
    sql = (
        f"SELECT DISTINCT time, {', '.join(tags)} FROM scheduled_workout "
        f"WHERE time >= '{start_ts}' AND time < '{end_ts}' "
        f"AND {tags[0]} IS NOT NULL"
    )
    try:
        rows = client.query(sql, language="sql").to_pylist()
        return {
            (str(r["time"])[:10], *("" if r.get(t) is None else str(r[t]) for t in tags))
            for r in rows
        }
    except Exception as exc:
        # A failed lookup only means fewer tombstones this run, never a false one. With
        # missing_column_expected (scheduled_id: like _query_active_calendar_event_keys, a
        # tag column only exists once a point carrying it was written, and an account with
        # only coach-plan workouts has none) it's routine, so info; otherwise (workout_name
        # always exists on a coach-plan account) it's a real query failure, so warning.
        column_missing = missing_column_expected and tags[0] in str(exc)
        log.log(
            logging.INFO if column_missing else logging.WARNING,
            "scheduled_workouts: %s key lookup failed%s, not tombstoning by it this run: %s",
            tags[0],
            " (column doesn't exist yet)" if column_missing else "",
            exc,
        )
        return set()


def _tombstones(keys: set[tuple[str, ...]], tags: tuple[str, ...], deleted_at: float) -> list[Any]:
    """deleted_at-only points overwriting each (date, *tag values) key's series in place."""
    points = []
    for date_str, *values in keys:
        p = Point("scheduled_workout").time(_day_ts(date.fromisoformat(date_str)))
        for tag, value in zip(tags, values, strict=True):
            p = p.tag(tag, value)
        points.append(p.field("deleted_at", deleted_at))
    return points


# Per-run cache of calendar month responses, shared by sync_scheduled_workouts and
# sync_calendar_events: both read the same calendar-service month endpoint, and the
# second would otherwise refetch the current+next months the first just fetched
# (2 extra Garmin calls every sync run). Reset at the start of every run_sync, and
# also whenever a different Garmin client is passed in, so one run's responses never
# leak into the next run, or between tests calling a sync function directly.
_calendar_month_cache: dict[str, Any] = {"garmin": None, "months": {}}


def _reset_calendar_month_cache() -> None:
    _calendar_month_cache["garmin"] = None
    _calendar_month_cache["months"] = {}


def _fetch_once(cache: dict[Any, Any], key: Any, fetch: Callable[[], Any]) -> Any:
    """cache[key], calling fetch() to fill it on a miss. Only successful results are cached,
    so a failed fetch is retried by the next caller. Sleeps only on a real fetch, to pace
    Garmin calls."""
    if key not in cache:
        try:
            cache[key] = fetch()
        finally:
            time.sleep(0.3)
    return cache[key]


def _get_calendar_month(garmin: Garmin, year: int, month: int) -> dict[str, Any]:
    """Return garmin.get_scheduled_workouts(year, month), fetching each month at most
    once per run. Only successful responses are cached, so a month that failed for
    one caller is retried by the next. Sleeps only on a real fetch."""
    if _calendar_month_cache["garmin"] is not garmin:
        _calendar_month_cache["garmin"] = garmin
        _calendar_month_cache["months"] = {}
    resp: dict[str, Any] = _fetch_once(
        _calendar_month_cache["months"],
        (year, month),
        lambda: garmin.get_scheduled_workouts(year, month) or {},
    )
    return resp


# Matches get_scheduled_workouts' max window (maxScheduledWorkoutsDays in tools/workouts.go;
# test_scheduled_workout_lookahead_matches_go_tool_max checks they agree), so a workout
# create_workout schedules anywhere in that window is visible to the tool. Covers
# 3-4 calendar months; sync_calendar_events fetches the same months anyway, and
# _get_calendar_month shares them, so the wider window costs no extra Garmin calls.
SCHEDULED_WORKOUT_LOOKAHEAD_DAYS = 60


def sync_scheduled_workouts(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    """Sync Garmin calendar workouts for the next SCHEDULED_WORKOUT_LOOKAHEAD_DAYS into InfluxDB."""
    today = date.today()
    horizon = today + timedelta(days=SCHEDULED_WORKOUT_LOOKAHEAD_DAYS)
    months = [(today.year, today.month)]
    while months[-1] != (horizon.year, horizon.month):
        year, month = months[-1]
        months.append((year + 1, 1) if month == 12 else (year, month + 1))

    points: list[Any] = []
    fresh_coach_keys: set[tuple[str, ...]] = set()
    fresh_self_keys: set[tuple[str, ...]] = set()
    had_error = False
    for year, month in months:
        try:
            raw = _get_calendar_month(garmin, year, month)
            items: list[Any] = raw.get("calendarItems") or []
            for item in items:
                # Calendar items include more than workouts (e.g. completed activities,
                # weight/bloodpressure entries, naps). Two distinct kinds of item count as
                # a real scheduled workout:
                #   - a non-null workoutId: a workout created via create_workout/the Garmin
                #     workout builder.
                #   - itemType == "fbtAdaptiveWorkout" with a trainingPlanId: a coach/training-plan-assigned workout.
                #     Verified live 2026-08-02 (bug found via a real coach plan): these always have workoutId=null —
                #     the real identifiers are trainingPlanId + workoutUuid instead. An earlier version of this filter
                #     only checked workoutId and silently dropped every coach-plan workout.
                try:
                    workout_id = item.get("workoutId")
                    is_coach_plan_item = (
                        item.get("itemType") == "fbtAdaptiveWorkout"
                        and item.get("trainingPlanId") is not None
                    )
                    if workout_id is None and not is_coach_plan_item:
                        continue
                    scheduled_id = item.get("id")
                    if scheduled_id is None:
                        log.warning("scheduled_workouts: item missing id, skipping")
                        # Same incompleteness class the had_error guard around
                        # tombstoning exists for (#105 review) — this item silently
                        # can't contribute to fresh_coach_keys, so tombstoning must
                        # not treat its absence as "the plan changed."
                        had_error = True
                        continue
                    date_str = item.get("date") or item.get("calendarDate")
                    if not date_str:
                        log.warning(
                            "scheduled_workouts: item %s missing date, skipping", scheduled_id
                        )
                        had_error = True
                        continue
                    scheduled_date = date.fromisoformat(str(date_str)[:10])

                    sport_val = str(item.get("sportTypeKey") or "")
                    name_val = str(item.get("title") or item.get("workoutName") or "")

                    # Coach-plan items are tagged by (sport, workout_name) instead of
                    # the calendar item's own id: that id churns every time the
                    # adaptive plan regenerates the day (same real workout, brand-new
                    # id), and since InfluxDB 3 Core has no DELETE, tagging on it piled
                    # up ever-growing ghost duplicates of every past regeneration.
                    # (sport, workout_name) collapses those correctly and, verified
                    # live 2026-08-07 against a real run + strength_training coach day,
                    # still keeps a genuine same-day multi-workout apart. Self-created
                    # items (real workoutId) keep the id tag — Garmin's own id is
                    # stable for those.
                    p = Point("scheduled_workout").time(_day_ts(scheduled_date))
                    if is_coach_plan_item:
                        p = p.tag("sport", sport_val).tag("workout_name", name_val)
                        fresh_coach_keys.add((scheduled_date.isoformat(), name_val, sport_val))
                    else:
                        p = p.tag("scheduled_id", str(scheduled_id)).tag("sport", sport_val)
                        fresh_self_keys.add(
                            (scheduled_date.isoformat(), str(scheduled_id), sport_val)
                        )

                    dur = _fval(item, "duration")
                    fields: dict[str, Any] = {
                        "workout_id": float(workout_id) if workout_id is not None else None,
                        "name": name_val,
                        "duration_s": dur
                        if dur is not None
                        else _fval(item, "estimatedDurationInSecs"),
                    }
                    # Explicitly clear any stale tombstone (#104): InfluxDB merges fields
                    # at the same series+timestamp rather than replacing the point
                    # outright, so if this exact key was tombstoned before and is now
                    # fresh again (coach plan reverted or regenerated identically, or a
                    # self-created workout moved back to a day it left), a write that
                    # omits deleted_at would leave the earlier deleted_at value in place
                    # and this real, current workout would stay permanently filtered out.
                    fields["deleted_at"] = 0.0
                    p, n = _add_fields(p, fields)
                    if n:
                        points.append(p)
                except Exception as exc:
                    had_error = True
                    log.warning("scheduled_workouts: item %s: %s", item.get("id"), exc)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            had_error = True
            log.warning("scheduled_workouts %d-%02d: %s", year, month, exc)

    # Tombstone coach-plan entries the fresh fetch no longer contains (#104): the
    # adaptive plan can rename a task for the same day/sport between sync runs
    # (Garmin's own recommendation engine regenerating a slot, e.g. "Recovery" ->
    # "Base") without changing sportTypeKey, and since scheduled_workout is keyed
    # by (sport, workout_name) for coach-plan items — deliberately, to keep a
    # genuine same-sport multi-session day apart, see the tag comment above — a
    # rename creates a brand-new series rather than overwriting the old one.
    # mergeTrainingPlanDetail then joins by (date, sport) only, so the stale
    # old-name row gets just as fully enriched as the current one and is
    # indistinguishable from real data to callers. There's no stable id to key on
    # instead (verified live 2026-08-16/17: the calendar item's own workoutUuid and
    # the adaptive-plan task's taskWorkout.workoutUuid disagree for the same real
    # task at the same moment, so neither is a trustworthy stable key). Since
    # InfluxDB 3 Core has no DELETE, mark the old row dead in place instead: same
    # tags + time so it overwrites the existing series, with only deleted_at set.
    #
    # Skipped entirely if any month/item failed to fetch or parse above (had_error):
    # fresh_coach_keys would then be incomplete for reasons that have nothing to do
    # with the plan actually changing, and diffing against it would wrongly
    # tombstone every real, still-current coach-plan entry that simply failed to
    # make it into this run's fetch. Safe to skip — a clean run next cycle will
    # tombstone anything genuinely stale then.
    if had_error:
        log.warning("scheduled_workouts: fetch/parse error this run, skipping tombstoning")
    else:
        # Starts tomorrow, not the 1st of the month (#105 review): today's own
        # calendar item can legitimately look different by the time this sync runs
        # (e.g. once a workout's underway/completed) for reasons that have nothing
        # to do with the plan changing, and today is the one date where this
        # write-side window would otherwise overlap tools/workouts.go's read-side
        # window (which starts from today forward) — a false tombstone here would
        # hide a real, still-relevant entry the same day it's scheduled. Nothing
        # needs a rename caught for a day that's already happening or done.
        window_start = today + timedelta(days=1)
        last_year, last_month = months[-1]
        window_end = (
            date(last_year + 1, 1, 1) if last_month == 12 else date(last_year, last_month + 1, 1)
        )
        deleted_at = datetime.now(UTC).timestamp()
        coach_tags = ("workout_name", "sport")
        stale_keys = (
            _query_active_keys(client, window_start, window_end, coach_tags) - fresh_coach_keys
        )
        points.extend(_tombstones(stale_keys, coach_tags, deleted_at))
        if stale_keys:
            log.info("scheduled_workouts: tombstoned %d stale coach-plan entries", len(stale_keys))
        # Same for self-created entries — now routine since create_workout's schedule_date
        # puts workouts on the calendar: one moved or deleted in Garmin Connect must not
        # linger at its old date. Keyed by Garmin's own scheduled_id (stable for these).
        # A separate query from the coach-plan one on purpose, not one combined SELECT: the
        # scheduled_id column doesn't exist until a self-created row has been written, and a
        # combined query naming it would then fail outright and take coach-plan tombstoning
        # (#104) down with it — same reasoning as _query_active_calendar_event_keys.
        self_tags = ("scheduled_id", "sport")
        stale_self = (
            _query_active_keys(
                client, window_start, window_end, self_tags, missing_column_expected=True
            )
            - fresh_self_keys
        )
        points.extend(_tombstones(stale_self, self_tags, deleted_at))
        if stale_self:
            log.info(
                "scheduled_workouts: tombstoned %d stale self-created entries", len(stale_self)
            )

    _write(client, points)
    log.info("scheduled_workouts: wrote %d points", len(points))


# Calendar events (e.g. races) are typically added months out, well past
# sync_scheduled_workouts' SCHEDULED_WORKOUT_LOOKAHEAD_DAYS (60-day) window — so they
# get their own, longer lookahead. Seven calendar months (current included) always covers
# get_calendar_events' 180-day window — six would stop short when run late in a
# month (e.g. Oct 31 + 180d = Apr 29, past a six-month fetch ending Apr 1).
CALENDAR_EVENT_LOOKAHEAD_MONTHS = 7

# completionTarget.unit -> meters. Only units actually observed live are mapped
# ("mile" on a personal event, "meter" on a subscribed shareable event — both 2026)
# (don't guess Garmin's other unit keys); an unmapped unit still has its raw
# completion_target_value/completion_target_unit stored, just no derived distance_m.
_COMPLETION_TARGET_UNIT_M = {"mile": 1609.344, "meter": 1.0}


def _calendar_event_key(event_id: Any, shareable_event_uuid: Any) -> tuple[str, str] | None:
    """(tag name, tag value) identifying a calendar event: Garmin's own id for a personal
    event, shareableEventUuid for a subscribed shareable event (which has no id)."""
    if event_id is not None:
        return ("event_id", str(event_id))
    if shareable_event_uuid:
        return ("shareable_event_uuid", str(shareable_event_uuid))
    return None


def _query_active_calendar_event_keys(
    client: InfluxDBClient3, start: date, end: date
) -> set[tuple[str, str, str]]:
    """Return the (date, tag name, tag value) keys currently stored in calendar_event in
    [start, end) — see _calendar_event_key.
    Same tombstone-lookup role as _query_active_keys — used by
    sync_calendar_events to find events that were moved to a different date or deleted.
    """
    start_ts = _day_ts(start).strftime("%Y-%m-%dT%H:%M:%SZ")
    end_ts = _day_ts(end).strftime("%Y-%m-%dT%H:%M:%SZ")
    keys: set[tuple[str, str, str]] = set()
    # One query per identifier tag, not a combined one: a tag column only exists in
    # InfluxDB once a point carrying it has been written, so on an account that has
    # never had a shareable event, a query naming shareable_event_uuid fails outright —
    # and a combined query would then lose every event_id key too, silently disabling
    # tombstoning for personal events. A failed lookup only means fewer tombstones this
    # run (never a false one), so each tag's failure is isolated and logged at info.
    for tag in ("event_id", "shareable_event_uuid"):
        sql = (
            f"SELECT DISTINCT time, {tag} FROM calendar_event "
            f"WHERE time >= '{start_ts}' AND time < '{end_ts}' AND {tag} IS NOT NULL"
        )
        try:
            rows = client.query(sql, language="sql").to_pylist()
        except Exception as exc:
            log.info(
                "calendar_events: %s key lookup failed (column may not exist yet), "
                "not tombstoning by it this run: %s",
                tag,
                exc,
            )
            continue
        for r in rows:
            if r.get(tag) is not None:
                keys.add((str(r["time"])[:10], tag, str(r[tag])))
    return keys


def sync_calendar_events(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    """Sync Garmin calendar events (itemType "event", e.g. races) for the next CALENDAR_EVENT_LOOKAHEAD_MONTHS.

    Event items come from the same calendar-service month response as scheduled
    workouts, as itemType == "event". Verified live 2026-10-09 against a real
    half-marathon event (non-null fields only):
        {
          "id": 28702711,
          "itemType": "event",
          "activityTypeId": 1,
          "title": "<race name>",
          "date": "2026-10-18",
          "isRace": true,
          "eventTimeLocal": {"startTimeHhMm": "08:00", "timeZoneId": "America/Chicago"},
          "completionTarget": {"value": 13.11, "unit": "mile", "unitType": "distance"},
          "primaryEvent": true
        }
    No goal time is present in this response. sportTypeKey is null on event items —
    activityTypeId is the only sport signal, stored raw rather than mapped.

    A second kind of event item was found live 2026-10-12: a shareable event the
    account subscribed to from Garmin's public event listing (here, the same race's
    public listing), with no id at all:
        {
          "itemType": "event",
          "activityTypeId": 1,
          "title": "<public race name>",
          "date": "2026-10-18",
          "url": "<event listing url>",
          "isRace": true,
          "location": "<city>",
          "shareableEventUuid": "<uuid>",
          "completionTarget": {"value": 21100.0, "unit": "meter", "unitType": "distance"},
          "shareableEvent": true,
          "subscribed": true
        }

    Tagged by event_id (Garmin's own id, stable for a real calendar event — unlike
    coach-plan items, it isn't regenerated), or by shareable_event_uuid for a
    shareable event, which has no id (see _calendar_event_key). An event moved to a new date writes a
    new point at the new time; the old-date point is tombstoned the same way
    sync_scheduled_workouts handles coach-plan renames (#104), since InfluxDB 3
    Core has no DELETE.
    """
    today = date.today()
    months: list[tuple[int, int]] = []
    year, month = today.year, today.month
    for _ in range(CALENDAR_EVENT_LOOKAHEAD_MONTHS):
        months.append((year, month))
        year, month = (year + 1, 1) if month == 12 else (year, month + 1)

    points: list[Any] = []
    fresh_keys: set[tuple[str, str, str]] = set()
    had_error = False
    for year, month in months:
        try:
            raw = _get_calendar_month(garmin, year, month)
            for item in raw.get("calendarItems") or []:
                if item.get("itemType") != "event":
                    continue
                try:
                    key = _calendar_event_key(item.get("id"), item.get("shareableEventUuid"))
                    date_str = item.get("date")
                    if key is None or not date_str:
                        log.warning(
                            "calendar_events: event item missing id/shareableEventUuid/date, "
                            "skipping"
                        )
                        had_error = True
                        continue
                    event_date = date.fromisoformat(str(date_str)[:10])

                    start_time = item.get("eventTimeLocal") or {}
                    target = item.get("completionTarget") or {}
                    target_value = _fval(target, "value")
                    target_unit = target.get("unit")
                    unit_m = _COMPLETION_TARGET_UNIT_M.get(str(target_unit))
                    distance_m = (
                        round(target_value * unit_m, 1)
                        if target_value is not None
                        and unit_m is not None
                        and target.get("unitType") == "distance"
                        else None
                    )
                    is_race = item.get("isRace")
                    primary = item.get("primaryEvent")
                    activity_type_id = item.get("activityTypeId")

                    p = Point("calendar_event").time(_day_ts(event_date)).tag(*key)
                    # Every field is written on every run, with 0.0/"" sentinels for
                    # absent values (never omitted): InfluxDB merges fields at the same
                    # tags+timestamp, so an omitted field would leave a stale prior value
                    # in place — e.g. a mile target changed to an unmapped unit would keep
                    # the old distance_m. Same reason deleted_at is always cleared (#104).
                    # The Go model's omitempty hides the sentinels from tool output.
                    p, _ = _add_fields(
                        p,
                        {
                            "name": str(item.get("title") or ""),
                            "activity_type_id": float(activity_type_id)
                            if activity_type_id is not None
                            else 0.0,
                            "is_race": 1.0 if is_race else 0.0,
                            "primary_event": 1.0 if primary else 0.0,
                            "start_time_local": str(start_time.get("startTimeHhMm") or ""),
                            "time_zone_id": str(start_time.get("timeZoneId") or ""),
                            "completion_target_value": target_value
                            if target_value is not None
                            else 0.0,
                            "completion_target_unit": str(target_unit or ""),
                            "distance_m": distance_m if distance_m is not None else 0.0,
                            "shareable_event": 1.0 if item.get("shareableEvent") else 0.0,
                            "subscribed": 1.0 if item.get("subscribed") else 0.0,
                            "location": str(item.get("location") or ""),
                            "url": str(item.get("url") or ""),
                            "deleted_at": 0.0,
                        },
                    )
                    points.append(p)
                    fresh_keys.add((event_date.isoformat(), *key))
                except Exception as exc:
                    had_error = True
                    log.warning("calendar_events: item %s: %s", item.get("id"), exc)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            had_error = True
            log.warning("calendar_events %d-%02d: %s", year, month, exc)

    # Same had_error guard as sync_scheduled_workouts' tombstoning — an incomplete
    # fetch must not be mistaken for a deleted event. Unlike that window, this one
    # includes today: the tomorrow-onward exception exists because today's workout
    # item changes once it's underway/completed, which doesn't apply to an event,
    # and get_calendar_events serves today — an event deleted or moved on its own
    # day would otherwise stay visible until tomorrow.
    if had_error:
        log.warning("calendar_events: fetch/parse error this run, skipping tombstoning")
    else:
        window_start = today
        last_year, last_month = months[-1]
        window_end = (
            date(last_year + 1, 1, 1) if last_month == 12 else date(last_year, last_month + 1, 1)
        )
        stale = _query_active_calendar_event_keys(client, window_start, window_end) - fresh_keys
        deleted_at = datetime.now(UTC).timestamp()
        for date_str, tag_name, tag_value in stale:
            points.append(
                Point("calendar_event")
                .time(_day_ts(date.fromisoformat(date_str)))
                .tag(tag_name, tag_value)
                .field("deleted_at", deleted_at)
            )
        if stale:
            log.info("calendar_events: tombstoned %d stale entries", len(stale))

    _write(client, points)
    log.info("calendar_events: wrote %d points", len(points))


TRAINING_PLAN_LOOKAHEAD_DAYS = 14

# targetType.workoutTargetTypeKey -> our target_type label. pace.zone's
# targetValueOne/Two are m/s (not the lactate-threshold 1/10th-scale quirk —
# verified live 2026-08-09: 2.972/2.638 m/s round-trip to a sane 5:36-6:19/km
# easy-pace range with no scaling needed).
_TARGET_TYPE_LABELS = {"heart.rate.zone": "heart_rate", "pace.zone": "pace"}


def _extract_workout_target(detail: dict[str, Any]) -> tuple[str, float | None, float | None]:
    """Pick "the" target range for a coach-plan workout (#97).

    A multi-step workout (e.g. a tempo run: warmup/interval/cooldown) can carry a
    different, wider range on its warmup/cooldown than its main interval — the
    interval's range is what the coach's flat description string (e.g. "137bpm",
    itself the midpoint of a 124-149bpm range, not a cap) actually refers to, so
    the first stepType=="interval" step with a real target wins; falls back to the
    first step with any real target at all if no interval step has one. Strength
    workouts wrap their steps in a RepeatGroupDTO — recurse into workoutSteps
    either way, since a repeat group's own targetType is always null.

    Returned as (label, lo, hi) with lo always <= hi — targetValueOne isn't
    consistently the lower bound (it's the LOW bpm bound for heart.rate.zone, but
    the FASTER/numerically-HIGHER m/s bound for pace.zone), so this normalizes to
    min/max rather than trusting One/Two's positional order. Only returned when
    BOTH targetValueOne and targetValueTwo are present — a step with just one of
    the pair (not observed live; Garmin may support an open-ended target) isn't
    treated as a usable range rather than guessing which bound is missing.

    Returns ("", None, None) if no step anywhere has a real, complete target (e.g.
    an all-no.target strength workout).
    """
    fallback: tuple[str, float | None, float | None] | None = None
    for segment in detail.get("workoutSegments") or []:
        for step in segment.get("workoutSteps") or []:
            for leaf in [step, *(step.get("workoutSteps") or [])]:
                target_type = leaf.get("targetType") or {}
                key = target_type.get("workoutTargetTypeKey") or ""
                label = _TARGET_TYPE_LABELS.get(key)
                if label is None:
                    continue
                one = _fval(leaf, "targetValueOne")
                two = _fval(leaf, "targetValueTwo")
                if one is None or two is None:
                    continue
                lo, hi = min(one, two), max(one, two)
                if leaf.get("stepType", {}).get("stepTypeKey") == "interval":
                    return label, lo, hi
                if fallback is None:
                    fallback = (label, lo, hi)
    return fallback or ("", None, None)


def sync_training_plan(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    """Sync per-day target detail from the active adaptive coach training plan.

    get_scheduled_workouts's calendarItems only carry a title/date for coach-plan
    items — no duration, pace/HR target, or rest-day flag, and rest days don't appear
    as calendar items at all. get_adaptive_training_plan_by_id(trainingPlanId) has the
    real per-day detail (estimatedDurationInSecs, estimatedDistanceInMeters,
    workoutDescription e.g. "21:00@5:10/km" or "137bpm", restDay, adaptivePlanPhases).
    Verified live 2026-08-03 against an active plan (trainingPlanId 46457367, endDate
    matching the account's race date) — found while checking whether a coach-assigned
    week was too light/heavy and realizing get_scheduled_workouts had no duration data
    to judge that with at all.

    The plan regenerates day to day (workoutUuid on the same calendarDate differs
    between captures a day apart) so this always re-syncs the near-term window fresh
    rather than tracking a watermark, same pattern as sync_scheduled_workouts.

    Also fetches the real HR/pace target range per non-rest-day task (#97) — see
    _extract_workout_target — since workoutDescription is only a flat display
    summary (e.g. "137bpm" is the midpoint of a real 124-149bpm range, not a cap).
    """
    today = date.today()
    horizon = today + timedelta(days=TRAINING_PLAN_LOOKAHEAD_DAYS)

    try:
        plans_raw = garmin.get_training_plans() or {}
    except (
        GarminConnectAuthenticationError,
        GarminConnectTooManyRequestsError,
        GarminConnectConnectionError,
    ):
        raise
    except Exception as exc:
        log.warning("training_plan: get_training_plans: %s", exc)
        return
    plans: list[Any] = plans_raw.get("trainingPlanList") or []

    points: list[Any] = []
    for plan in plans:
        plan_id = plan.get("trainingPlanId")
        if plan_id is None:
            continue
        end_date_str = plan.get("endDate")
        if end_date_str:
            try:
                if date.fromisoformat(str(end_date_str)[:10]) < today:
                    continue  # plan already ended
            except ValueError:
                pass

        try:
            detail = garmin.get_adaptive_training_plan_by_id(plan_id) or {}
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            log.warning("training_plan: get_adaptive_training_plan_by_id(%s): %s", plan_id, exc)
            continue

        phases: list[Any] = detail.get("adaptivePlanPhases") or []

        for task in detail.get("taskList") or []:
            try:
                date_str = task.get("calendarDate")
                if not date_str:
                    continue
                task_date = date.fromisoformat(str(date_str)[:10])
                if not (today <= task_date <= horizon):
                    continue

                w = task.get("taskWorkout") or {}
                rest_day = bool(w.get("restDay"))
                sport_val = str((w.get("sportType") or {}).get("sportTypeKey") or "")

                phase = ""
                for ph in phases:
                    ph_start, ph_end = ph.get("startDate"), ph.get("endDate")
                    if ph_start and ph_end and str(ph_start)[:10] <= date_str <= str(ph_end)[:10]:
                        phase = str(ph.get("trainingPhase") or "")
                        break

                # sport is part of the series key, not just a field: verified live
                # 2026-08-07 that a coach day can carry two taskList entries for the
                # same calendarDate (running + strength_training). Without sport here,
                # the second write silently overwrote the first on the same
                # (training_plan_id, date) series.
                p = (
                    Point("training_plan_task")
                    .tag("training_plan_id", str(plan_id))
                    .tag("sport", sport_val)
                    .time(_day_ts(task_date))
                )
                fields: dict[str, Any] = {
                    "name": str(w.get("workoutName") or ("Rest" if rest_day else "")),
                    "description": str(w.get("workoutDescription") or ""),
                    "duration_s": _fval(w, "estimatedDurationInSecs"),
                    "distance_m": _fval(w, "estimatedDistanceInMeters"),
                    "rest_day": 1.0 if rest_day else 0.0,
                    "workout_phrase": str(w.get("workoutPhrase") or ""),
                    "phase": phase,
                }

                # Real HR/pace target range, not just the flat description string
                # (#97) — a rest day has no target to enrich, and its taskWorkout
                # doesn't carry a real workoutUuid, so skip the extra API call
                # entirely rather than guess at that shape.
                workout_uuid = w.get("workoutUuid")
                if not rest_day and workout_uuid:
                    try:
                        wdetail = garmin.connectapi(f"workout-service/fbt-adaptive/{workout_uuid}")
                        target_type, target_lo, target_hi = _extract_workout_target(wdetail or {})
                        if target_type:
                            fields["target_type"] = target_type
                            fields["target_lo"] = target_lo
                            fields["target_hi"] = target_hi
                    except (
                        GarminConnectAuthenticationError,
                        GarminConnectTooManyRequestsError,
                        GarminConnectConnectionError,
                    ):
                        raise
                    except Exception as exc:
                        log.warning("training_plan: fbt-adaptive %s: %s", workout_uuid, exc)
                    time.sleep(0.2)

                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
            except (
                GarminConnectAuthenticationError,
                GarminConnectTooManyRequestsError,
                GarminConnectConnectionError,
            ):
                raise
            except Exception as exc:
                log.warning("training_plan: task %s: %s", task.get("calendarDate"), exc)

        time.sleep(0.3)

    _write(client, points)
    log.info("training_plan: wrote %d points", len(points))


# Verified 2026-07-28 against /workout-service/workout/types (live, this account) plus
# empirical round-trip tests (upload a probe workout, read back the sportType Garmin
# actually assigned). The original values here were guessed and wrong: strength_training
# was 13 (which Garmin resolves to "rucking"), swimming was 5 (which is strength_training).
# "walking" has no working sportTypeId — both the documented enum value (11, "mobility")
# and a community-library guess (17) came back nulled by Garmin on round-trip. Dropped
# from validSports in tools/workouts.go until a working ID is found — see issue tracker.
_SPORT_TYPES: dict[str, dict[str, Any]] = {
    "running": {"sportTypeId": 1, "sportTypeKey": "running"},
    "cycling": {"sportTypeId": 2, "sportTypeKey": "cycling"},
    "swimming": {"sportTypeId": 4, "sportTypeKey": "swimming"},
    "strength_training": {"sportTypeId": 5, "sportTypeKey": "strength_training"},
}

# Verified 2026-07-28 against /workout-service/workout/types (live, this account).
# "rest" and "repeat" are internal-only: synthesized by _build_garmin_repeat_group,
# never read from a user-supplied step["type"] (create_workout's validStepTypes never
# includes them — see tools/workouts.go).
_STEP_TYPES: dict[str, dict[str, Any]] = {
    "warmup": {"stepTypeId": 1, "stepTypeKey": "warmup"},
    "cooldown": {"stepTypeId": 2, "stepTypeKey": "cooldown"},
    "interval": {"stepTypeId": 3, "stepTypeKey": "interval"},
    "recovery": {"stepTypeId": 4, "stepTypeKey": "recovery"},
    "rest": {"stepTypeId": 5, "stepTypeKey": "rest"},
    "repeat": {"stepTypeId": 6, "stepTypeKey": "repeat"},
    "steady": {"stepTypeId": 7, "stepTypeKey": "other"},
}


def _build_garmin_step(
    order: int, step: dict[str, Any], child_step_id: int | None = None
) -> dict[str, Any]:
    step_type = _STEP_TYPES.get(step["type"], {"stepTypeId": 7, "stepTypeKey": "other"})

    duration_s = step.get("duration_s")
    distance_m = step.get("distance_m")
    reps = step.get("reps")
    if duration_s is not None:
        end_condition: dict[str, Any] = {"conditionTypeId": 2, "conditionTypeKey": "time"}
        end_value: float | None = float(duration_s)
    elif distance_m is not None:
        end_condition = {"conditionTypeId": 3, "conditionTypeKey": "distance"}
        end_value = float(distance_m)
    elif reps is not None:
        end_condition = {"conditionTypeId": 10, "conditionTypeKey": "reps"}
        end_value = float(reps)
    else:
        end_condition = {"conditionTypeId": 1, "conditionTypeKey": "lap.button"}
        end_value = None

    target_type: dict[str, Any] | None
    target_val1: float | None
    target_val2: float | None
    hr_low = step.get("target_hr_low_bpm")
    hr_high = step.get("target_hr_high_bpm")
    pace_fast = step.get("target_pace_zone_fast_s_per_km")
    pace_slow = step.get("target_pace_zone_slow_s_per_km")
    if pace_fast is not None and pace_slow is not None:
        # Verified live 2026-10-12 (probe upload of 300/315 s/km + get_workout_by_id
        # readback + delete_workout): pace.zone is workoutTargetTypeId 6, values are
        # plain m/s stored unconverted (3.3333333/3.1746032 back), and targetValueOne
        # holds the FASTER (higher m/s) bound — same polarity as coach-plan pace.zone
        # steps (see _extract_workout_target).
        target_type = {"workoutTargetTypeId": 6, "workoutTargetTypeKey": "pace.zone"}
        target_val1 = 1000.0 / float(pace_fast)
        target_val2 = 1000.0 / float(pace_slow)
    elif hr_low is not None and hr_high is not None:
        # Verified live 2026-10-19 (probe upload + get_workout_by_id readback +
        # delete_workout): heart.rate.zone's targetValueOne/Two are a bpm range, LOW then
        # HIGH, stored as sent (135.0/150.0) — the same form Garmin Coach plan steps and
        # app-built workouts use. The old target_hr_zone sent a zone number here, which the
        # watch showed as a "2-3 bpm" target (#112).
        target_type = {
            "workoutTargetTypeId": 4,
            "workoutTargetTypeKey": "heart.rate.zone",
        }
        target_val1 = float(hr_low)
        target_val2 = float(hr_high)
    elif step["type"] == "rest":
        # Real Garmin rest steps serialize targetType as null, not the explicit
        # no.target object exercise-performing steps get (confirmed against a
        # captured real hand-built strength workout).
        target_type = None
        target_val1 = None
        target_val2 = None
    else:
        target_type = {"workoutTargetTypeId": 1, "workoutTargetTypeKey": "no.target"}
        target_val1 = None
        target_val2 = None

    weight_kg = step.get("weight_kg")

    return {
        "type": "ExecutableStepDTO",
        "stepOrder": order,
        "stepType": step_type,
        "childStepId": child_step_id,
        "endCondition": end_condition,
        "endConditionValue": end_value,
        "targetType": target_type,
        "targetValueOne": target_val1,
        "targetValueTwo": target_val2,
        # Real Garmin workouts serialize these as explicit nulls when unset, not
        # omitted keys (confirmed against a captured real workout) — match that
        # shape for round-trip fidelity.
        "category": step.get("category"),
        "exerciseName": step.get("exercise_name"),
        "description": step.get("description"),
        # Verified live 2026-08-09 (probe upload + readback + delete): sending a
        # bare {"unitKey": "kilogram"} with no unitId is enough — Garmin resolves
        # unitId (8) and factor (1000.0) itself — and weightValue round-trips
        # unconverted (60.0 in -> 60.0 kg out), i.e. it's already kilograms.
        "weightValue": float(weight_kg) if weight_kg is not None else None,
        "weightUnit": {"unitKey": "kilogram"} if weight_kg is not None else None,
    }


def _build_garmin_repeat_group(
    order: int, group_index: int, step: dict[str, Any]
) -> tuple[dict[str, Any], int, int]:
    """Wrap one exercise+rest pair in a RepeatGroupDTO, repeated `sets` times.

    Mirrors the real structure of a hand-built Garmin strength workout: the repeat
    group itself and each of its children get sequential, gap-free stepOrder values
    (confirmed against a captured real workout — order is a running counter across
    the whole step tree, not reset per group), and share one childStepId marking
    group membership.

    Scope limit: only ever builds one [exercise, rest] pair — Garmin's real format
    also supports multi-exercise supersets (a captured real hand-built workout had
    groups with two alternating exercises), but WorkoutStep/create_workout don't
    model that; adding it would need changes to the queue schema and validation,
    not just this function.

    Returns (repeat_group_dto, next_order, next_group_index) so the caller can keep
    threading both counters through subsequent steps.
    """
    sets = step.get("sets")
    rest_s = step.get("rest_s")
    if not sets or sets < 2 or not rest_s or rest_s <= 0:
        # create_workout's validation should have caught this before queueing — a
        # malformed item (hand-edited queue file, a future second producer) reaching
        # here fails loudly instead of raising an unlabeled KeyError.
        raise ValueError(f"invalid sets/rest_s for repeat group: sets={sets!r} rest_s={rest_s!r}")

    exercise_step = _build_garmin_step(order + 1, step, child_step_id=group_index)
    rest_step = _build_garmin_step(
        order + 2, {"type": "rest", "duration_s": rest_s}, child_step_id=group_index
    )
    children = [exercise_step, rest_step]

    group = {
        "type": "RepeatGroupDTO",
        "stepOrder": order,
        "stepType": _STEP_TYPES["repeat"],
        "childStepId": group_index,
        "numberOfIterations": sets,
        "workoutSteps": children,
        "endCondition": {"conditionTypeId": 7, "conditionTypeKey": "iterations"},
        "endConditionValue": float(sets),
    }
    return group, order + 1 + len(children), group_index + 1


def _build_garmin_workout(item: dict[str, Any]) -> dict[str, Any]:
    sport = item.get("sport", "running")
    # No silent fallback: an unrecognized sport previously defaulted to sportTypeId 1
    # ("running") while keeping the wrong key, which Garmin resolves by ID — silently
    # mislabeling the workout as a run. Fail loudly instead: sync_pending_workouts keeps the
    # item and retries it (quarantining it after _FAILURE_GIVE_UP), rather than uploading
    # it with a wrong sport.
    if sport not in _SPORT_TYPES:
        raise ValueError(f"unsupported workout sport {sport!r} — no verified Garmin sportTypeId")
    sport_type = _SPORT_TYPES[sport]

    steps: list[dict[str, Any]] = []
    order = 1
    group_index = 1
    for raw_step in item.get("steps") or []:
        # Route to the repeat-group builder whenever either field is present, not
        # just "sets >= 2" — that way an invalid combination (sets=1, or rest_s
        # without sets) fails loudly via _build_garmin_repeat_group's own
        # validation instead of silently falling through to a flat step that
        # drops the sets/rest_s fields entirely.
        if raw_step.get("sets") is not None or raw_step.get("rest_s") is not None:
            group, order, group_index = _build_garmin_repeat_group(order, group_index, raw_step)
            steps.append(group)
        else:
            steps.append(_build_garmin_step(order, raw_step))
            order += 1

    return {
        "workoutId": None,
        "ownerId": None,
        "workoutName": item.get("name", ""),
        "description": None,
        "sportType": sport_type,
        "workoutSegments": [
            {
                "segmentOrder": 1,
                "sportType": sport_type,
                "workoutSteps": steps,
            }
        ],
    }


# Spool directory of one <id>.json file per queued workout, shared with the Go MCP server
# (tools/workouts.go appendToQueue). Go only ever creates new files (a ".item-*.tmp" dotfile,
# then linked — or, where hard links aren't supported, renamed — into place as <id>.json);
# this sidecar only rewrites or removes files it has read. Neither side ever
# writes a file the other could be writing, so no lock is needed.
_QUEUE_DIR_NAME = "workout_queue"


# The single shared file the spool replaced — see _check_legacy_queue.
_LEGACY_QUEUE_FILE = "workout_queue.json"


class _QueueWriteError(Exception):
    """A queue item's outcome couldn't be persisted. Processing must stop: carrying on would
    upload more workouts whose completion can't be recorded, so the next run would upload
    them again as duplicates."""


def _queue_dir() -> Path:
    return DATA_DIR / _QUEUE_DIR_NAME


def _write_queue_item(path: Path, item: dict[str, Any]) -> None:
    """Atomically replace path with item. fsynced (file, then directory) so a crash or
    power loss can't leave a truncated file — which would be quarantined, losing the
    workout or, worse, its uploaded_workout_id (causing a duplicate upload)."""
    tmp = path.with_name(f".{path.name}.tmp")
    try:
        with open(tmp, "w") as fh:
            fh.write(json.dumps(item))
            fh.flush()
            os.fsync(fh.fileno())
        tmp.replace(path)
        dir_fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(dir_fd)
        except OSError as exc:
            # Some filesystems can't fsync a directory; the rename itself still happened.
            if exc.errno not in (errno.EINVAL, errno.ENOTSUP):
                raise
        finally:
            os.close(dir_fd)
    except OSError as exc:
        raise _QueueWriteError(f"cannot update queue item {path.name}: {exc}") from exc


def _remove_queue_item(path: Path) -> None:
    try:
        path.unlink(missing_ok=True)
    except OSError as exc:
        raise _QueueWriteError(f"cannot remove queue item {path.name}: {exc}") from exc


def _quarantine(path: Path, reason: str) -> None:
    """Rename a queue file that can never be processed to <name>.invalid: out of the *.json
    glob so it isn't re-logged every run, but kept for a human to look at. Raises
    _QueueWriteError if the rename fails: the item would otherwise stay live with nothing
    recorded, re-sending the same failing request every run with no give-up."""
    log.error(
        "pending_workouts: %s %s — moving it aside as %s.invalid", path.name, reason, path.name
    )
    try:
        path.replace(path.with_name(f"{path.name}.invalid"))
    except OSError as exc:
        raise _QueueWriteError(f"cannot move {path.name} aside: {exc}") from exc


def _check_legacy_queue() -> None:
    """Deal with the pre-spool single workout_queue.json, which nothing writes any more.

    Deliberately no automatic migration: making one safe against an old MCP server still
    writing the file mid-migration (duplicate uploads, lost items, id collisions) took more
    machinery than the one case it serves — the MCP server and this sidecar are deployed
    together, and the file is normally empty ("[]") by the time either is upgraded. An
    empty one is removed; anything else is left untouched and reported every run, for a
    human to move into the spool dir (one <id>.json file per item) by hand.
    """
    legacy = DATA_DIR / _LEGACY_QUEUE_FILE
    if not legacy.exists():
        return
    try:
        items = json.loads(legacy.read_text())
    except (json.JSONDecodeError, OSError) as exc:
        log.error(
            "pending_workouts: cannot read legacy %s: %s — move it aside by hand", legacy, exc
        )
        return
    if items == []:
        try:
            legacy.unlink()
        except OSError as exc:
            log.warning("pending_workouts: cannot remove empty legacy %s: %s", legacy, exc)
        return
    log.error(
        "pending_workouts: legacy %s still holds %d item(s) the spool queue won't upload — move "
        "each into %s/ as its own <id>.json file, then delete it",
        legacy,
        len(items) if isinstance(items, list) else 1,
        _queue_dir(),
    )


def _queued_item_paths() -> list[Path]:
    """Queue files in creation order: Go's ids are "<UnixNano>-<8 hex>" (newQueueID), fixed
    length, so sorting by name sorts by creation time. If the id format ever gets a
    variable-length part, this ordering must change with it. Dotfiles are in-progress
    writes (Go's and our own)."""
    if not _queue_dir().is_dir():
        return []
    paths = [p for p in _queue_dir().glob("*.json") if not p.name.startswith(".")]
    return sorted(paths, key=lambda p: p.name)


def _write_workout_detail(client: InfluxDBClient3, resp: dict[str, Any]) -> None:
    """Persist the step tree Garmin assigned to a just-uploaded workout.

    garmin.upload_workout's response is confirmed (live probe upload + delete,
    2026-08-07) to have the identical shape as garmin.get_workout_by_id — including
    the Garmin-assigned workoutId and the full workoutSegments[0].workoutSteps tree,
    with server-side stepId values and any Garmin-side normalization already applied.
    Written here instead of a separate get_workout_by_id round-trip so
    get_workout_detail's rollout verification (confirming what Garmin actually stored,
    not just what create_workout sent) needs no extra API call. Only workoutId /
    workoutName / sportType.sportTypeKey / workoutSteps are kept — the rest of the
    response carries real account PII (author displayName, profile image filenames)
    with no reason to land in InfluxDB.
    """
    workout_id = resp.get("workoutId")
    if workout_id is None:
        return
    segments = resp.get("workoutSegments") or []
    steps = segments[0].get("workoutSteps", []) if segments else []
    p = Point("workout_detail").tag("workout_id", str(workout_id)).time(datetime.now(UTC))
    fields: dict[str, Any] = {
        "name": str(resp.get("workoutName") or ""),
        "sport": str((resp.get("sportType") or {}).get("sportTypeKey") or ""),
        "steps_json": json.dumps(steps),
    }
    p, n = _add_fields(p, fields)
    if n:
        _write(client, [p])


_API_ERROR_STATUS_RE = re.compile(r"API Error (\d{3})")

# 4xx statuses meaning "this request can never succeed as sent". Everything else in 4xx is
# treated as transient: 403 is what Cloudflare's bot wall returns (see "Python sidecar is
# required" in CLAUDE.md), 408 is a timeout, 409 a conflict that can clear.
_PERMANENT_4XX = frozenset({400, 404, 410, 422})


def _http_status(exc: Exception) -> int | None:
    """HTTP status behind a garminconnect error. Checks a status_code / response.status_code
    attribute first, then the "API Error NNN" message garminconnect's client raises with —
    the same order as garminconnect's own private _extract_status_code, reimplemented rather
    than imported so a garminconnect upgrade that renames it can't break this sidecar."""
    for status in (
        getattr(exc, "status_code", None),
        getattr(getattr(exc, "response", None), "status_code", None),
    ):
        if isinstance(status, int):
            return status
    m = _API_ERROR_STATUS_RE.search(str(exc))
    return int(m.group(1)) if m else None


def _garmin_write_error_kind(exc: Exception) -> str:
    """Classify a failure from upload_workout/schedule_workout: "stop", "permanent",
    "transient" or "other".

    Both call garmin.client.post directly, bypassing garminconnect's _handle_api_errors
    decorator, so every HTTP status >= 400 — 401 after a refresh and 429 included — surfaces
    as GarminConnectConnectionError("API Error NNN - ...") (garminconnect 0.3.6,
    client.py _run_request), and network failures surface raw from its requests.Session
    (requests.Timeout etc., all OSError subclasses):
      - "stop": auth failure or rate limit — make no more Garmin calls this run.
      - "permanent": 400/404/410/422 (e.g. 404 on a workout deleted in the app, 400 on a body
        or date Garmin rejects) — retrying the same request can't succeed.
      - "transient": 5xx, any other 4xx, timeout, network error — worth retrying next run.
      - "other": not a Garmin or network error at all (e.g. a bug building this item's
        request) — specific to this item, so it's kept but doesn't hold up the queue.
    """
    if isinstance(exc, GarminConnectAuthenticationError | GarminConnectTooManyRequestsError):
        return "stop"
    if isinstance(exc, OSError):
        return "transient"
    if not isinstance(exc, GarminConnectConnectionError):
        return "other"
    status = _http_status(exc)
    if status in (401, 429):
        return "stop"
    if status in _PERMANENT_4XX:
        return "permanent"
    return "transient"


def _raise_if_stop(exc: Exception) -> None:
    """Re-raise exc as its typed garminconnect error if it's a "stop" failure (auth/rate
    limit). Every failed Garmin call in _process_queue_item goes through this (via
    _record_failure) — swallowing a 401 would leave the run going with a dead session
    instead of the main loop re-logging in."""
    if _garmin_write_error_kind(exc) == "stop":
        raise _stop_error(exc) from exc


def _stop_error(exc: Exception) -> Exception:
    """The typed garminconnect exception a "stop" failure should propagate as. What matters
    is 401: the main loop re-logs-in on GarminConnectAuthenticationError, whereas a raw
    GarminConnectConnectionError("API Error 401") would only be logged and retried with the
    same dead session. (A 429 becomes GarminConnectTooManyRequestsError for an accurate log
    line only — the main loop treats it like any connection error, retrying at the normal
    SYNC_SCHEDULE interval with no extra backoff.)"""
    if isinstance(exc, GarminConnectAuthenticationError | GarminConnectTooManyRequestsError):
        return exc
    if _http_status(exc) == 401:
        return GarminConnectAuthenticationError(str(exc))
    return GarminConnectTooManyRequestsError(str(exc))


def _schedule_uploaded_workout(garmin: Garmin, workout_id: Any, sched_date: str, name: Any) -> None:
    """Put an uploaded workout on the Garmin calendar on sched_date.

    garminconnect's schedule_workout POSTs {"date": "YYYY-MM-DD"} to
    workout-service/schedule/<workoutId>. Response confirmed live 2026-10-17 (scheduled a real
    uploaded workout, read it back via get_scheduled_workout_by_id) — see
    schemas/workout_schedule.schema.json:
        {
          "workoutScheduleId": 1811236098,   # id unschedule_workout takes
          "workout": {...},                  # same shape as upload_workout's response
          "calendarDate": "2026-10-17",
          "createdDate": "2026-10-17",
          "ownerId": ..., "race": false, "protected": false, ...
        }
    """
    sched = garmin.schedule_workout(workout_id, sched_date)
    log.info(
        "pending_workouts: scheduled %r for %s (schedule id %s)",
        name,
        sched_date,
        sched.get("workoutScheduleId") if isinstance(sched, dict) else None,
    )


def _already_scheduled(
    garmin: Garmin,
    workout_id: Any,
    sched_date: str,
    cache: dict[Any, Any],
) -> bool:
    """Whether workout_id is already on the calendar on sched_date.

    schedule_workout isn't idempotent: a retry after a call Garmin applied but whose
    response was lost (timeout), or whose queue removal then failed to persist, would add
    a second calendar entry. The calendar month's items carry the scheduled workout's
    workoutId and date — confirmed live 2026-10-17:
        {"id": 1811236098, "itemType": "workout", "workoutId": 1732218826,
         "date": "2026-10-17", "trainingPlanId": 0, ...}
    (id == schedule_workout's workoutScheduleId).

    cache is sync_pending_workouts' per-run cache (months keyed by (year, month)), not the
    shared _get_calendar_month cache: a month fetched there before this run's scheduling
    would be stale here.
    """
    d = date.fromisoformat(sched_date)
    month = _fetch_once(
        cache, (d.year, d.month), lambda: garmin.get_scheduled_workouts(d.year, d.month) or {}
    )
    return any(
        # str() both sides: a hand-edited queue file may hold the id as a string.
        str(it.get("workoutId")) == str(workout_id) and str(it.get("date", ""))[:10] == sched_date
        for it in month.get("calendarItems") or []
    )


_SCHEDULE_DATE_RE = re.compile(r"\d{4}-\d{2}-\d{2}")


def _schedule_expired(sched_date: str) -> bool:
    """Whether sched_date is over everywhere — i.e. before today in UTC-12, the same
    cutoff create_workout's validateScheduleDate uses. Scheduling onto a past day only adds
    a phantom missed workout."""
    return date.fromisoformat(sched_date) < datetime.now(timezone(timedelta(hours=-12))).date()


def _valid_schedule_date(item: dict[str, Any]) -> str | None:
    """item's schedule_date if it's strictly YYYY-MM-DD (what garminconnect's schedule_workout
    accepts, and what create_workout enforces via time.DateOnly), else None. A bad one means a
    hand-edited queue file. (date.fromisoformat alone would also accept "20261017".)"""
    sched_date = item.get("schedule_date")
    if not sched_date:
        return None
    try:
        if not _SCHEDULE_DATE_RE.fullmatch(str(sched_date)):
            raise ValueError
        date.fromisoformat(str(sched_date))
    except ValueError:
        return None
    return str(sched_date)


# Failure handling for a queue item, deliberately kept to two fields on the item, cleared
# when it makes progress (uploads):
# - "attempts": failed runs in a row. A transient failure (5xx/403/network) normally means
#   Garmin is down, so it holds up the rest of the queue — but only for the first
#   _BLOCKING_ATTEMPTS runs; after that it's more likely this item (e.g. a body Garmin always
#   500s on, the DataIntegrityViolationException seen live) and it stops blocking the others.
# - "first_failure_at": when that streak began. An item still failing _FAILURE_GIVE_UP later
#   is quarantined. Time-based, so a long Garmin outage can't quarantine everything early
#   however short SYNC_SCHEDULE is.
# A permanent rejection (400/404/410/422) is quarantined at once. Quarantine
# (<id>.json.invalid) never deletes a workout definition.
_BLOCKING_ATTEMPTS = 3
_FAILURE_GIVE_UP = timedelta(days=7)


def _parse_utc(value: Any) -> datetime | None:
    """An ISO timestamp as an aware UTC datetime (naive taken as UTC), or None."""
    try:
        parsed = datetime.fromisoformat(str(value))
    except (TypeError, ValueError):
        return None
    return parsed.astimezone(UTC) if parsed.tzinfo else parsed.replace(tzinfo=UTC)


def _record_failure(
    path: Path, item: dict[str, Any], exc: Exception, doing: str, read: bool = False
) -> str:
    """Handle a failed Garmin call made for item. Returns "stop" if the rest of the queue
    should wait for the next run, else "continue". Auth/rate-limit failures raise the typed
    garminconnect error instead (see _raise_if_stop). A failed *read* (read=True) is never
    permanent: a 4xx on a calendar lookup says nothing about whether the item can succeed."""
    _raise_if_stop(exc)
    kind = _garmin_write_error_kind(exc)
    if read and kind == "permanent":
        kind = "transient"
    uploaded = item.get("uploaded_workout_id")
    # An uploaded item only ever fails on scheduling: say so, or the user may re-create a
    # workout that's already in their library.
    unscheduled_note = (
        f"; workout {uploaded} is in the Garmin workout library but NOT on the calendar for "
        f"{item.get('schedule_date')} — schedule it by hand"
        if uploaded
        else ""
    )
    if kind == "permanent":
        # Quarantined, not deleted: if the 4xx is really Garmin-wide (a moved endpoint, an
        # edge rejecting every POST), every queued workout would be lost in one run.
        _quarantine(path, f"was rejected by Garmin {doing} ({exc}){unscheduled_note}")
        return "continue"
    now = datetime.now(UTC)
    first = _parse_utc(item.get("first_failure_at")) or now
    if now - first >= _FAILURE_GIVE_UP:
        _quarantine(path, f"has been failing since {first.isoformat()} ({exc}){unscheduled_note}")
        return "continue"
    try:
        attempts = max(0, int(item.get("attempts") or 0)) + 1
    except (TypeError, ValueError):
        attempts = 1
    _write_queue_item(path, {**item, "attempts": attempts, "first_failure_at": first.isoformat()})
    # "other" (not a Garmin error — bad item data, a bug) is specific to this item, so it
    # never holds up the rest.
    blocking = kind == "transient" and attempts <= _BLOCKING_ATTEMPTS
    log.warning(
        "pending_workouts: failed %s %s (attempt %d): %s — keeping it%s",
        doing,
        item.get("id"),
        attempts,
        exc,
        " and the rest of the queue for the next run" if blocking else " in queue",
    )
    return "stop" if blocking else "continue"


def sync_pending_workouts(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    """Upload queued workouts to Garmin Connect, scheduling any that carry schedule_date.

    Once an item with schedule_date has uploaded, its file records uploaded_workout_id
    before schedule_workout is called, so a retry only re-attempts the schedule — never a
    second upload. A schedule retry checks the calendar first, so one Garmin applied but
    whose response was lost isn't added twice. (An *upload* whose response is lost after
    Garmin stored it is retried as a fresh upload: rare, and at worst one extra library
    workout — accepted rather than detected.) See _record_failure for failure handling.
    """
    _check_legacy_queue()
    try:
        _queue_dir().mkdir(parents=True, exist_ok=True)
    except OSError as exc:
        log.warning("pending_workouts: cannot create %s: %s", _queue_dir(), exc)
    paths = _queued_item_paths()
    if not paths:
        return

    counts = {"uploaded": 0, "scheduled": 0, "failed": 0}
    # Per-run cache of calendar months for _already_scheduled — not the shared
    # _get_calendar_month cache, which could hold a month from before this run's scheduling.
    cache: dict[Any, Any] = {}
    try:
        for path in paths:
            try:
                item = json.loads(path.read_text())
            except OSError as exc:
                counts["failed"] += 1
                log.warning("pending_workouts: cannot read %s: %s — skipping", path.name, exc)
                continue
            except json.JSONDecodeError as exc:
                counts["failed"] += 1
                _quarantine(path, f"is not valid JSON ({exc})")
                continue
            if not isinstance(item, dict):
                counts["failed"] += 1
                _quarantine(path, "is not a JSON object")
                continue
            if _process_queue_item(garmin, client, path, item, counts, cache) == "stop":
                break
    finally:
        log.info(
            "pending_workouts: uploaded %d, scheduled %d, %d failed (see warnings/errors)",
            counts["uploaded"],
            counts["scheduled"],
            counts["failed"],
        )


def _process_queue_item(
    garmin: Garmin,
    client: InfluxDBClient3,
    path: Path,
    item: dict[str, Any],
    counts: dict[str, int],
    cache: dict[Any, Any],
) -> str:
    """Process one queue item. Returns "stop" to leave the rest of the queue for the next
    run, else "continue"; auth/rate-limit failures and _QueueWriteError propagate."""
    item_id = item.get("id")
    workout_id = item.get("uploaded_workout_id")
    sched_date = _valid_schedule_date(item)
    if item.get("schedule_date") and sched_date is None:
        # Logged once, then moved aside on the item so a retry doesn't repeat the ERROR.
        log.error(
            "pending_workouts: %s has invalid schedule_date %r — ignoring it, can't schedule",
            item_id,
            item["schedule_date"],
        )
        item = {**item, "invalid_schedule_date": item["schedule_date"]}
        del item["schedule_date"]
        _write_queue_item(path, item)
    if sched_date is not None and _schedule_expired(sched_date):
        if workout_id is None:
            log.warning(
                "pending_workouts: %r was for %s, which has passed — uploading it unscheduled",
                item.get("name"),
                sched_date,
            )
        sched_date = None
    if workout_id is not None and sched_date is None:
        # Already uploaded, and the date is gone (passed or invalid). Re-uploading would
        # duplicate the workout, so there's nothing left to do with it.
        log.warning(
            "pending_workouts: %r (workout %s) is in the Garmin workout library; it may not be "
            "on the calendar — check it by hand. Dropping it from the queue.",
            item.get("name"),
            workout_id,
        )
        _remove_queue_item(path)
        return "continue"

    if workout_id is None:
        try:
            resp = garmin.upload_workout(_build_garmin_workout(item))
        except Exception as exc:
            counts["failed"] += 1
            return _record_failure(path, item, exc, "uploading")
        counts["uploaded"] += 1
        log.info("pending_workouts: uploaded %r (id %s)", item.get("name"), item_id)
        workout_id = resp.get("workoutId") if isinstance(resp, dict) else None
        # Record the upload first — before the InfluxDB detail write or scheduling — so the
        # window in which a crash or kill causes a duplicate upload next run is as small as
        # it can be. A _QueueWriteError here propagates.
        if sched_date is None or workout_id is None:
            _remove_queue_item(path)
        else:
            item = {k: v for k, v in item.items() if k not in ("attempts", "first_failure_at")}
            item["uploaded_workout_id"] = workout_id
            _write_queue_item(path, item)
        if isinstance(resp, dict):
            try:
                _write_workout_detail(client, resp)
            except Exception as exc:
                log.warning(
                    "pending_workouts: failed to write workout_detail for %s: %s", item_id, exc
                )
        if sched_date is None:
            return "continue"
        if workout_id is None:
            log.error(
                "pending_workouts: %r uploaded but the response has no workoutId — it is in "
                "the Garmin workout library but NOT scheduled for %s; schedule it by hand",
                item.get("name"),
                sched_date,
            )
            return "continue"
    else:
        # A retry: a previous schedule_workout may have been applied with only its response
        # (or the queue update after it) lost.
        assert sched_date is not None  # uploaded + no date returned above
        try:
            done = _already_scheduled(garmin, workout_id, sched_date, cache)
        except Exception as exc:
            counts["failed"] += 1
            return _record_failure(path, item, exc, "checking the calendar for", read=True)
        if done:
            log.info(
                "pending_workouts: %r already on the calendar for %s — not scheduling again",
                item.get("name"),
                sched_date,
            )
            counts["scheduled"] += 1
            _remove_queue_item(path)
            return "continue"

    assert sched_date is not None  # every unscheduled path returned above
    try:
        _schedule_uploaded_workout(garmin, workout_id, sched_date, item.get("name"))
    except Exception as exc:
        counts["failed"] += 1
        return _record_failure(path, item, exc, f"scheduling (for {sched_date})")
    counts["scheduled"] += 1
    _remove_queue_item(path)
    return "continue"


def sync_respiration(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    start = _last_synced(state, "respiration")
    end = date.today()
    log.info("respiration: %s → %s", start, end)

    points = []
    d = start
    _first_err: date | None = None
    while d <= end:
        try:
            raw = garmin.get_respiration_data(d.isoformat())
            if raw:
                p = Point("respiration").time(_day_ts(d))
                fields = {
                    "avg_waking_brpm": _fval(raw, "avgWakingRespirationValue"),
                    "avg_sleep_brpm": _fval(raw, "avgSleepRespirationValue"),
                    "highest_brpm": _fval(raw, "highestRespirationValue"),
                    "lowest_brpm": _fval(raw, "lowestRespirationValue"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("respiration %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = max((_first_err - timedelta(days=1)) if _first_err else end, start)
    _write(client, points)
    _advance_state(state, "respiration", watermark, had_error=_first_err is not None)
    log.info("respiration: wrote %d points", len(points))


# ── Main ───────────────────────────────────────────────────────────────────────

SYNC_FUNCS = [
    sync_activities,
    sync_activity_details,
    sync_daily_stats,
    sync_sleep,
    sync_hrv,
    sync_training_readiness,
    sync_training_status,
    sync_performance,
    sync_lactate_threshold,
    sync_respiration,
    sync_pending_workouts,  # upload queued workouts before reading the calendar back
    sync_scheduled_workouts,
    sync_calendar_events,
    sync_training_plan,
]


def run_sync(garmin: Garmin, client: InfluxDBClient3) -> None:
    state = _load_state()
    _reset_calendar_month_cache()
    for fn in SYNC_FUNCS:
        try:
            fn(garmin, client, state)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise  # handled by main loop
        except Exception as exc:
            log.error("%s failed: %s", fn.__name__, exc)


if __name__ == "__main__":
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(message)s",
    )
    log.info(
        "Waypoint sync starting — interval %ds, backfill %d days",
        SYNC_INTERVAL_S,
        BACKFILL_DAYS,
    )
    if _sync_schedule_warning:
        log.warning(_sync_schedule_warning)

    client = _influx_client()

    garmin = None
    _login_backoff = 60
    while garmin is None:
        try:
            garmin = _login_and_wrap()
        except Exception as exc:
            log.error(
                "Initial login failed: %s. "
                "If MFA is required, set GARMIN_MFA_CODE or run sync/auth.py to seed the token. "
                "Retrying in %ds.",
                exc,
                _login_backoff,
            )
            time.sleep(_login_backoff)
            _login_backoff = min(_login_backoff * 2, 1800)

    while True:
        log.info("── sync run start ──")
        try:
            run_sync(garmin, client)
        except GarminConnectAuthenticationError:
            log.warning("Auth expired — re-authenticating")
            try:
                garmin = _login_and_wrap()
            except Exception as exc:
                log.error("Re-auth failed: %s — will retry next interval", exc)
        except (GarminConnectConnectionError, GarminConnectTooManyRequestsError) as exc:
            log.error("Garmin error: %s — retry next interval", exc)
        except Exception as exc:
            log.error("Sync run failed: %s", exc)
        log.info("── sync run done — sleeping %ds ──", SYNC_INTERVAL_S)
        time.sleep(SYNC_INTERVAL_S)
