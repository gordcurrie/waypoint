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

import json
import logging
import os
import re
import time
from datetime import UTC, date, datetime, timedelta
from pathlib import Path
from typing import Any

from garminconnect import (
    Garmin,
    GarminConnectAuthenticationError,
    GarminConnectConnectionError,
    GarminConnectTooManyRequestsError,
)
from influxdb_client_3 import InfluxDBClient3, Point

# ── Config ─────────────────────────────────────────────────────────────────────

GARMIN_EMAIL = os.environ["GARMIN_EMAIL"]
GARMIN_PASSWORD = os.environ["GARMIN_PASSWORD"]
GARMIN_MFA_CODE = os.environ.get("GARMIN_MFA_CODE", "")  # one-shot MFA for automated envs
INFLUXDB_URL = os.environ["INFLUXDB_URL"]
INFLUXDB_DB = os.environ.get("INFLUXDB_DATABASE", "garmin")
INFLUXDB_TOKEN = os.environ.get("INFLUXDB_TOKEN", "")
BACKFILL_DAYS = int(os.environ.get("BACKFILL_DAYS", "90"))
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


# Garmin returns HRV status as a string enum; map to float for InfluxDB.
_HRV_STATUS: dict[str, float] = {"BALANCED": 2.0, "UNBALANCED": 1.0, "POOR": 0.0}


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
                    "hrv_status": _HRV_STATUS.get(str(item.get("hrvStatus", "")), None),
                    "sleep_score": _fval(item, "sleepScore"),
                    "recovery_time_h": _scale(_fval(item, "recoveryTime"), 1.0 / 60.0),
                    "acw_ratio": _fval(item, "acwRatio"),
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
            if raw:
                # get_training_status can return a list; normalise like training_readiness
                item = raw[0] if isinstance(raw, list) else raw
                latest = (
                    (item.get("latestTrainingStatusData") or item)
                    if isinstance(item, dict)
                    else item
                )
                p = Point("training_status").time(_day_ts(d))
                status_str = str(latest.get("trainingStatus", "")).lower()
                fields = {
                    "status_num": status_num.get(status_str, None),
                    "vo2max_running": _fval(latest, "latestRunningVO2MaxValue"),
                    "vo2max_cycling": _fval(latest, "latestCyclingVO2MaxValue"),
                    "fitness_age": _fval(latest, "fitnessAge"),
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
    """VO2 max / fitness age per day. Lactate threshold written to its own measurement."""
    start = _last_synced(state, "performance")
    end = date.today()
    log.info("performance: %s → %s", start, end)

    points = []
    d = start
    _first_err: date | None = None
    while d <= end:
        try:
            raw = garmin.get_max_metrics(d.isoformat())
            if raw:
                item = raw[0] if isinstance(raw, list) else raw
                generic = item.get("generic") or item
                p = Point("performance").time(_day_ts(d))
                fields = {
                    "vo2max": _fval(generic, "vo2MaxPreciseValue"),
                    "fitness_age": _fval(generic, "fitnessAge"),
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
    """Most-recent lactate threshold result — separate measurement avoids timestamp collision with performance."""
    log.info("lactate_threshold: fetching most recent")
    try:
        lt = garmin.get_lactate_threshold()
        if not lt:
            return
        # Use the actual test date from the API response; fall back to today
        raw_date = lt.get("testDate") or lt.get("date") or lt.get("dateTime")
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
        # paceThreshold unit: garmin biometric-service returns s/m; multiply by 1000
        # to convert to s/km (matching the field name). Verify against a real payload
        # if this account ever completes an LT test.
        fields = {
            "lt_hr_bpm": _fval(lt, "heartRateThreshold"),
            "lt_pace_s_per_km": _scale(_fval(lt, "paceThreshold"), 1000.0),
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
    """Fetch per-lap splits and HR zone distribution for each recent activity."""
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
                    "avg_power_w": _fval(lap, "avgPower"),
                    "elevation_gain_m": _fval(lap, "totalAscent"),
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

    watermark = max((_first_err - timedelta(days=1)) if _first_err else end, start)
    _write(client, points)
    _advance_state(state, "activity_details", watermark, had_error=_first_err is not None)
    log.info("activity_details: wrote %d points for %d activities", len(points), len(activities))


def sync_scheduled_workouts(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    """Sync Garmin calendar workouts for the current and next month into InfluxDB."""
    today = date.today()
    months = [(today.year, today.month)]
    # Always include next month to cover a 14-day lookahead that spans a boundary.
    if today.month == 12:
        months.append((today.year + 1, 1))
    else:
        months.append((today.year, today.month + 1))

    points: list[Any] = []
    for year, month in months:
        try:
            raw = garmin.get_scheduled_workouts(year, month) or {}
            items: list[Any] = raw.get("calendarItems") or []
            for item in items:
                # Calendar items include more than workouts (e.g. races, notes).
                # Only sync items that have a workoutId — those are scheduled workouts.
                try:
                    workout_id = item.get("workoutId")
                    if workout_id is None:
                        continue
                    scheduled_id = item.get("id")
                    if scheduled_id is None:
                        log.warning("scheduled_workouts: item missing id, skipping")
                        continue
                    date_str = item.get("date") or item.get("calendarDate")
                    if not date_str:
                        log.warning(
                            "scheduled_workouts: item %s missing date, skipping", scheduled_id
                        )
                        continue
                    scheduled_date = date.fromisoformat(str(date_str)[:10])

                    sport_raw = item.get("sport") or item.get("activityType")
                    sport_val = (
                        str(sport_raw.get("typeKey") or "")
                        if isinstance(sport_raw, dict)
                        else str(sport_raw or "")
                    )

                    p = (
                        Point("scheduled_workout")
                        .tag("scheduled_id", str(scheduled_id))
                        .tag("sport", sport_val)
                        .time(_day_ts(scheduled_date))
                    )
                    dur = _fval(item, "duration")
                    fields: dict[str, Any] = {
                        "workout_id": float(workout_id),
                        "name": str(item.get("title") or item.get("workoutName") or ""),
                        "duration_s": dur
                        if dur is not None
                        else _fval(item, "estimatedDurationInSecs"),
                    }
                    p, n = _add_fields(p, fields)
                    if n:
                        points.append(p)
                except Exception as exc:
                    log.warning("scheduled_workouts: item %s: %s", item.get("id"), exc)
        except (
            GarminConnectAuthenticationError,
            GarminConnectTooManyRequestsError,
            GarminConnectConnectionError,
        ):
            raise
        except Exception as exc:
            log.warning("scheduled_workouts %d-%02d: %s", year, month, exc)
        time.sleep(0.3)

    _write(client, points)
    log.info("scheduled_workouts: wrote %d points", len(points))


_SPORT_TYPES: dict[str, dict[str, Any]] = {
    "running": {"sportTypeId": 1, "sportTypeKey": "running"},
    "cycling": {"sportTypeId": 2, "sportTypeKey": "cycling"},
    "swimming": {"sportTypeId": 5, "sportTypeKey": "swimming"},
    "walking": {"sportTypeId": 11, "sportTypeKey": "walking"},
    "strength_training": {"sportTypeId": 13, "sportTypeKey": "strength_training"},
}

_STEP_TYPES: dict[str, dict[str, Any]] = {
    "warmup": {"stepTypeId": 1, "stepTypeKey": "warmup"},
    "cooldown": {"stepTypeId": 2, "stepTypeKey": "cooldown"},
    "interval": {"stepTypeId": 3, "stepTypeKey": "interval"},
    "recovery": {"stepTypeId": 4, "stepTypeKey": "recovery"},
    "steady": {"stepTypeId": 7, "stepTypeKey": "other"},
}


def _build_garmin_step(order: int, step: dict[str, Any]) -> dict[str, Any]:
    step_type = _STEP_TYPES.get(step["type"], {"stepTypeId": 7, "stepTypeKey": "other"})

    duration_s = step.get("duration_s")
    distance_m = step.get("distance_m")
    if duration_s is not None:
        end_condition: dict[str, Any] = {"conditionTypeId": 2, "conditionTypeKey": "time"}
        end_value: float | None = float(duration_s)
    elif distance_m is not None:
        end_condition = {"conditionTypeId": 3, "conditionTypeKey": "distance"}
        end_value = float(distance_m)
    else:
        end_condition = {"conditionTypeId": 1, "conditionTypeKey": "lap.button"}
        end_value = None

    hr_zone = step.get("target_hr_zone")
    if hr_zone is not None:
        target_type: dict[str, Any] = {
            "workoutTargetTypeId": 4,
            "workoutTargetTypeKey": "heart.rate.zone",
        }
        target_val1: float | None = float(hr_zone)
        target_val2: float | None = float(hr_zone)
    else:
        target_type = {"workoutTargetTypeId": 1, "workoutTargetTypeKey": "no.target"}
        target_val1 = None
        target_val2 = None

    return {
        "stepOrder": order,
        "stepType": step_type,
        "endCondition": end_condition,
        "endConditionValue": end_value,
        "targetType": target_type,
        "targetValueOne": target_val1,
        "targetValueTwo": target_val2,
        "description": step.get("description") or "",
    }


def _build_garmin_workout(item: dict[str, Any]) -> dict[str, Any]:
    sport = item.get("sport", "running")
    sport_type = _SPORT_TYPES.get(sport, {"sportTypeId": 1, "sportTypeKey": sport})
    steps = [_build_garmin_step(i + 1, s) for i, s in enumerate(item.get("steps") or [])]
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


def _save_queue(items: list[dict[str, Any]]) -> None:
    queue_file = DATA_DIR / "workout_queue.json"
    tmp = queue_file.with_suffix(".json.tmp")
    tmp.write_text(json.dumps(items))
    tmp.rename(queue_file)


def sync_pending_workouts(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    """Upload queued workouts to Garmin Connect. Items stay in queue on failure (retry next run)."""
    queue_file = DATA_DIR / "workout_queue.json"
    if not queue_file.exists():
        return

    try:
        items: list[dict[str, Any]] = json.loads(queue_file.read_text())
    except (json.JSONDecodeError, OSError) as exc:
        log.warning("pending_workouts: cannot read queue: %s", exc)
        return

    if not items:
        return

    remaining: list[dict[str, Any]] = []
    uploaded = 0
    try:
        for i, item in enumerate(items):
            try:
                workout = _build_garmin_workout(item)
                garmin.upload_workout(workout)
                uploaded += 1
                log.info("pending_workouts: uploaded %r (id %s)", item.get("name"), item.get("id"))
            except (
                GarminConnectAuthenticationError,
                GarminConnectTooManyRequestsError,
                GarminConnectConnectionError,
            ):
                remaining.extend(items[i:])  # current item + all unprocessed
                raise
            except Exception as exc:
                log.warning(
                    "pending_workouts: failed to upload %s: %s — keeping in queue",
                    item.get("id"),
                    exc,
                )
                remaining.append(item)
    finally:
        _save_queue(remaining)
    log.info("pending_workouts: uploaded %d, %d remaining in queue", uploaded, len(remaining))


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
]


def run_sync(garmin: Garmin, client: InfluxDBClient3) -> None:
    state = _load_state()
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
            garmin = _garmin_login()
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
                garmin = _garmin_login()
            except Exception as exc:
                log.error("Re-auth failed: %s — will retry next interval", exc)
        except (GarminConnectConnectionError, GarminConnectTooManyRequestsError) as exc:
            log.error("Garmin error: %s — retry next interval", exc)
        except Exception as exc:
            log.error("Sync run failed: %s", exc)
        log.info("── sync run done — sleeping %ds ──", SYNC_INTERVAL_S)
        time.sleep(SYNC_INTERVAL_S)
