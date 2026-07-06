"""
Garmin Connect → InfluxDB 3 sync sidecar.

Data synced: activities, daily_stats, sleep, hrv, training_readiness,
             training_status, performance (VO2 max / LT), respiration.

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
from datetime import date, datetime, timedelta, timezone
from pathlib import Path
from typing import Optional

from garminconnect import (
    Garmin,
    GarminConnectAuthenticationError,
    GarminConnectConnectionError,
    GarminConnectTooManyRequestsError,
)
from influxdb_client_3 import InfluxDBClient3, Point

# ── Config ─────────────────────────────────────────────────────────────────────

GARMIN_EMAIL    = os.environ["GARMIN_EMAIL"]
GARMIN_PASSWORD = os.environ["GARMIN_PASSWORD"]
INFLUXDB_URL    = os.environ["INFLUXDB_URL"]
INFLUXDB_DB     = os.environ.get("INFLUXDB_DATABASE", "garmin")
INFLUXDB_TOKEN  = os.environ.get("INFLUXDB_TOKEN", "")
BACKFILL_DAYS   = int(os.environ.get("BACKFILL_DAYS", "90"))
DATA_DIR        = Path(os.environ.get("DATA_DIR", "/data"))
TOKEN_STORE     = str(DATA_DIR / "garmin_auth")
STATE_FILE      = DATA_DIR / "sync_state.json"

# Parse interval from */N cron pattern; default 30 min
_cron           = os.environ.get("SYNC_SCHEDULE", "*/30 * * * *")
_interval_match = re.match(r"\*/(\d+)", _cron.split()[0])
SYNC_INTERVAL_S = int(_interval_match.group(1)) * 60 if _interval_match else 1800

log = logging.getLogger(__name__)


# ── State ──────────────────────────────────────────────────────────────────────

def _load_state() -> dict:
    if STATE_FILE.exists():
        return json.loads(STATE_FILE.read_text())
    return {}


def _save_state(state: dict) -> None:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    STATE_FILE.write_text(json.dumps(state, indent=2))


def _last_synced(state: dict, key: str) -> date:
    if key in state:
        return date.fromisoformat(state[key])
    return date.today() - timedelta(days=BACKFILL_DAYS)


# ── Auth ───────────────────────────────────────────────────────────────────────

def _garmin_login() -> Garmin:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    try:
        garmin = Garmin()
        garmin.login(TOKEN_STORE)
        log.info("Logged in via saved token")
    except (FileNotFoundError, GarminConnectAuthenticationError):
        log.info("No valid token — authenticating with credentials")
        garmin = Garmin(email=GARMIN_EMAIL, password=GARMIN_PASSWORD)
        garmin.login()
        garmin.garth.dump(TOKEN_STORE)
        log.info("Saved auth token to %s", TOKEN_STORE)
    return garmin


# ── InfluxDB ───────────────────────────────────────────────────────────────────

def _influx_client() -> InfluxDBClient3:
    return InfluxDBClient3(
        host=INFLUXDB_URL,
        database=INFLUXDB_DB,
        token=INFLUXDB_TOKEN,
    )


def _write(client: InfluxDBClient3, points: list) -> None:
    if points:
        client.write(record=points)


# ── Helpers ────────────────────────────────────────────────────────────────────

def _parse_gmt(ts: str) -> datetime:
    return datetime.strptime(ts, "%Y-%m-%d %H:%M:%S").replace(tzinfo=timezone.utc)


def _day_ts(d: date) -> datetime:
    return datetime(d.year, d.month, d.day, tzinfo=timezone.utc)


def _fval(data, *keys) -> Optional[float]:
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


def _add_fields(p: Point, fields: dict) -> tuple[Point, int]:
    """Add non-None fields to point; return (point, count_added)."""
    count = 0
    for k, v in fields.items():
        if v is not None:
            p = p.field(k, v)
            count += 1
    return p, count


# ── Sync functions ─────────────────────────────────────────────────────────────

def sync_activities(garmin: Garmin, client: InfluxDBClient3, state: dict) -> None:
    start = _last_synced(state, "activities")
    end   = date.today()
    log.info("activities: %s → %s", start, end)

    activities = garmin.get_activities_by_date(start.isoformat(), end.isoformat())

    points = []
    for a in activities:
        ts_str = a.get("startTimeGMT")
        if not ts_str:
            continue

        sport = (a.get("activityType") or {}).get("typeKey", "unknown")
        p = Point("activity").tag("sport", sport).time(_parse_gmt(ts_str))

        fields: dict = {
            "activity_id":      float(a["activityId"]) if a.get("activityId") else None,
            "distance_m":       _fval(a, "distance"),
            "duration_s":       _fval(a, "duration"),
            "avg_hr_bpm":       _fval(a, "averageHR"),
            "max_hr_bpm":       _fval(a, "maxHR"),
            "calories_kcal":    _fval(a, "calories"),
            "elevation_gain_m": _fval(a, "elevationGain"),
            "avg_speed_m_s":    _fval(a, "avgSpeed"),
            "training_load":    _fval(a, "activityTrainingLoad"),
            "aerobic_te":       _fval(a, "aerobicTrainingEffect"),
            "anaerobic_te":     _fval(a, "anaerobicTrainingEffect"),
            "vo2max":           _fval(a, "vO2MaxValue"),
        }
        if sport in ("running", "trail_running", "treadmill_running", "indoor_running"):
            fields.update({
                "cadence_avg_spm":         _fval(a, "averageRunningCadenceInStepsPerMinute"),
                "ground_contact_time_ms":  _fval(a, "groundContactTime"),
                "vertical_oscillation_mm": _fval(a, "avgVerticalOscillation"),
                "stride_length_mm":        _fval(a, "avgStrideLength"),
                "vertical_ratio_pct":      _fval(a, "avgVerticalRatio"),
                "avg_power_w":             _fval(a, "avgPower"),
            })

        p, n = _add_fields(p, fields)
        if n:
            points.append(p)

    _write(client, points)
    state["activities"] = end.isoformat()
    _save_state(state)
    log.info("activities: wrote %d points", len(points))


def sync_daily_stats(garmin: Garmin, client: InfluxDBClient3, state: dict) -> None:
    start = _last_synced(state, "daily_stats")
    end   = date.today()
    log.info("daily_stats: %s → %s", start, end)

    points = []
    d = start
    while d <= end:
        try:
            s = garmin.get_stats(d.isoformat())
            if s:
                p = Point("daily_stats").time(_day_ts(d))
                fields = {
                    "steps":                  _fval(s, "totalSteps"),
                    "resting_hr_bpm":         _fval(s, "restingHeartRate"),
                    "body_battery_max":        _fval(s, "bodyBatteryHighestValue"),
                    "body_battery_min":        _fval(s, "bodyBatteryLowestValue"),
                    "stress_avg":             _fval(s, "averageStressLevel"),
                    "active_calories":        _fval(s, "activeKilocalories"),
                    "total_calories":         _fval(s, "totalKilocalories"),
                    "floors_ascended":        _fval(s, "floorsAscended"),
                    "vigorous_intensity_min": _fval(s, "vigorousIntensityMinutes"),
                    "moderate_intensity_min": _fval(s, "moderateIntensityMinutes"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except Exception as exc:
            log.warning("daily_stats %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    _write(client, points)
    state["daily_stats"] = end.isoformat()
    _save_state(state)
    log.info("daily_stats: wrote %d points", len(points))


def sync_sleep(garmin: Garmin, client: InfluxDBClient3, state: dict) -> None:
    start = _last_synced(state, "sleep")
    end   = date.today()
    log.info("sleep: %s → %s", start, end)

    points = []
    d = start
    while d <= end:
        try:
            raw = garmin.get_sleep_data(d.isoformat())
            if raw:
                daily  = raw.get("dailySleepDTO") or {}
                scores = raw.get("sleepScores") or {}
                p = Point("sleep").time(_day_ts(d))
                fields = {
                    "total_sleep_s":      _fval(daily, "sleepTimeSeconds"),
                    "deep_sleep_s":       _fval(daily, "deepSleepSeconds"),
                    "light_sleep_s":      _fval(daily, "lightSleepSeconds"),
                    "rem_sleep_s":        _fval(daily, "remSleepSeconds"),
                    "awake_s":            _fval(daily, "awakeSleepSeconds"),
                    "sleep_score":        _fval(scores, "overall", "value"),
                    "avg_hrv_ms":         _fval(daily, "avgOvernightHrv"),
                    "avg_spo2_pct":       _fval(daily, "averageSpO2Value"),
                    "avg_breathing_rate": _fval(daily, "averageRespirationValue"),
                    "avg_stress":         _fval(daily, "avgSleepStress"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except Exception as exc:
            log.warning("sleep %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    _write(client, points)
    state["sleep"] = end.isoformat()
    _save_state(state)
    log.info("sleep: wrote %d points", len(points))


def sync_hrv(garmin: Garmin, client: InfluxDBClient3, state: dict) -> None:
    start = _last_synced(state, "hrv")
    end   = date.today()
    log.info("hrv: %s → %s", start, end)

    status_num = {"BALANCED": 2.0, "UNBALANCED": 1.0, "POOR": 0.0}
    points = []
    d = start
    while d <= end:
        try:
            raw = garmin.get_hrv_data(d.isoformat())
            if raw:
                summary = raw.get("hrvSummary") or raw
                p = Point("hrv").time(_day_ts(d))
                fields = {
                    "weekly_avg_ms":    _fval(summary, "weeklyAvg"),
                    "last_night_ms":    _fval(summary, "lastNight"),
                    "last_5min_high_ms": _fval(summary, "lastNight5MinHigh"),
                    "status":        status_num.get(str(summary.get("status", "")), None),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except Exception as exc:
            log.warning("hrv %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    _write(client, points)
    state["hrv"] = end.isoformat()
    _save_state(state)
    log.info("hrv: wrote %d points", len(points))


def sync_training_readiness(garmin: Garmin, client: InfluxDBClient3, state: dict) -> None:
    start = _last_synced(state, "training_readiness")
    end   = date.today()
    log.info("training_readiness: %s → %s", start, end)

    points = []
    d = start
    while d <= end:
        try:
            raw = garmin.get_training_readiness(d.isoformat())
            if raw:
                item = raw[0] if isinstance(raw, list) else raw
                p = Point("training_readiness").time(_day_ts(d))
                fields = {
                    "score":           _fval(item, "score"),
                    "hrv_status":      _fval(item, "hrvStatus"),
                    "sleep_score":     _fval(item, "sleepScore"),
                    "recovery_time_h": _fval(item, "recoveryTime"),
                    "acw_ratio":       _fval(item, "acwRatio"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except Exception as exc:
            log.warning("training_readiness %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    _write(client, points)
    state["training_readiness"] = end.isoformat()
    _save_state(state)
    log.info("training_readiness: wrote %d points", len(points))


def sync_training_status(garmin: Garmin, client: InfluxDBClient3, state: dict) -> None:
    start = _last_synced(state, "training_status")
    end   = date.today()
    log.info("training_status: %s → %s", start, end)

    status_num = {
        "peaking": 5.0, "maintaining": 4.0, "productive": 3.0,
        "recovery": 2.0, "detraining": 1.0, "overreaching": 0.0,
    }
    points = []
    d = start
    while d <= end:
        try:
            raw = garmin.get_training_status(d.isoformat())
            if raw:
                latest = raw.get("latestTrainingStatusData") or raw
                p = Point("training_status").time(_day_ts(d))
                status_str = str(latest.get("trainingStatus", "")).lower()
                fields = {
                    "status_num":     status_num.get(status_str, None),
                    "vo2max_running": _fval(latest, "latestRunningVO2MaxValue"),
                    "vo2max_cycling": _fval(latest, "latestCyclingVO2MaxValue"),
                    "fitness_age":    _fval(latest, "fitnessAge"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except Exception as exc:
            log.warning("training_status %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    _write(client, points)
    state["training_status"] = end.isoformat()
    _save_state(state)
    log.info("training_status: wrote %d points", len(points))


def sync_performance(garmin: Garmin, client: InfluxDBClient3, state: dict) -> None:
    """VO2 max / fitness age per day + lactate threshold (most recent, once per run)."""
    start = _last_synced(state, "performance")
    end   = date.today()
    log.info("performance: %s → %s", start, end)

    points = []
    d = start
    while d <= end:
        try:
            raw = garmin.get_max_metrics(d.isoformat())
            if raw:
                item    = raw[0] if isinstance(raw, list) else raw
                generic = item.get("generic") or item
                p = Point("performance").time(_day_ts(d))
                fields = {
                    "vo2max":      _fval(generic, "vo2MaxPreciseValue"),
                    "fitness_age": _fval(generic, "fitnessAge"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except Exception as exc:
            log.warning("performance %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    try:
        lt = garmin.get_lactate_threshold()
        if lt:
            p = Point("performance").time(_day_ts(end))
            fields = {
                "lt_hr_bpm":        _fval(lt, "heartRateThreshold"),
                "lt_pace_s_per_km": _fval(lt, "paceThreshold"),
            }
            p, n = _add_fields(p, fields)
            if n:
                points.append(p)
    except Exception as exc:
        log.warning("lactate_threshold: %s", exc)

    _write(client, points)
    state["performance"] = end.isoformat()
    _save_state(state)
    log.info("performance: wrote %d points", len(points))


def sync_respiration(garmin: Garmin, client: InfluxDBClient3, state: dict) -> None:
    start = _last_synced(state, "respiration")
    end   = date.today()
    log.info("respiration: %s → %s", start, end)

    points = []
    d = start
    while d <= end:
        try:
            raw = garmin.get_respiration_data(d.isoformat())
            if raw:
                p = Point("respiration").time(_day_ts(d))
                fields = {
                    "avg_waking_brpm": _fval(raw, "avgWakingRespirationValue"),
                    "avg_sleep_brpm":  _fval(raw, "avgSleepRespirationValue"),
                    "highest_brpm":    _fval(raw, "highestRespirationValue"),
                    "lowest_brpm":     _fval(raw, "lowestRespirationValue"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except Exception as exc:
            log.warning("respiration %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    _write(client, points)
    state["respiration"] = end.isoformat()
    _save_state(state)
    log.info("respiration: wrote %d points", len(points))


# ── Main ───────────────────────────────────────────────────────────────────────

SYNC_FUNCS = [
    sync_activities,
    sync_daily_stats,
    sync_sleep,
    sync_hrv,
    sync_training_readiness,
    sync_training_status,
    sync_performance,
    sync_respiration,
]


def run_sync(garmin: Garmin, client: InfluxDBClient3) -> None:
    state = _load_state()
    for fn in SYNC_FUNCS:
        try:
            fn(garmin, client, state)
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

    garmin = _garmin_login()
    client = _influx_client()

    while True:
        log.info("── sync run start ──")
        try:
            run_sync(garmin, client)
        except (GarminConnectConnectionError, GarminConnectTooManyRequestsError) as exc:
            log.error("Garmin connection error: %s — retry next interval", exc)
        except Exception as exc:
            log.error("Sync run failed: %s", exc)
        log.info("── sync run done — sleeping %ds ──", SYNC_INTERVAL_S)
        time.sleep(SYNC_INTERVAL_S)
