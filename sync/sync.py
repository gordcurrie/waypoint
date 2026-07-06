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
        return date.fromisoformat(state[key])
    return date.today() - timedelta(days=BACKFILL_DAYS)


# ── Auth ───────────────────────────────────────────────────────────────────────


def _garmin_login() -> Garmin:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    try:
        garmin = Garmin()
        garmin.login(TOKEN_STORE)
        log.info("Logged in via saved token")
        return garmin
    except Exception:
        # Covers FileNotFoundError, corrupt JSON, expired token, garth network errors
        pass

    log.info("No valid token — authenticating with credentials")
    mfa_cb = (lambda: GARMIN_MFA_CODE) if GARMIN_MFA_CODE else None
    garmin = Garmin(email=GARMIN_EMAIL, password=GARMIN_PASSWORD, prompt_mfa=mfa_cb)
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


def _write(client: InfluxDBClient3, points: list[Any]) -> None:
    if points:
        client.write(record=points)


# ── Helpers ────────────────────────────────────────────────────────────────────


def _parse_gmt(ts: str) -> datetime:
    # Garmin sometimes appends fractional seconds; strip before parsing.
    # Replace T separator so both "2026-07-06 10:30:00" and "2026-07-06T10:30:00" work.
    return datetime.strptime(ts.split(".")[0].replace("T", " "), "%Y-%m-%d %H:%M:%S").replace(
        tzinfo=UTC
    )


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


def _add_fields(p: Point, fields: dict[str, Any]) -> tuple[Point, int]:
    """Add non-None fields to point; return (point, count_added)."""
    count = 0
    for k, v in fields.items():
        if v is not None:
            p = p.field(k, v)
            count += 1
    return p, count


def _advance_state(state: dict[str, Any], key: str, points: list[Any], end: date) -> None:
    """Advance watermark only if data was written; guards against silent all-fail loops."""
    if points:
        state[key] = end.isoformat()
        _save_state(state)
    else:
        log.warning("%s: no data written for run ending %s — state not advanced", key, end)


# ── Sync functions ─────────────────────────────────────────────────────────────


def sync_activities(garmin: Garmin, client: InfluxDBClient3, state: dict[str, Any]) -> None:
    start = _last_synced(state, "activities")
    end = date.today()
    log.info("activities: %s → %s", start, end)

    activities = garmin.get_activities_by_date(start.isoformat(), end.isoformat())

    points = []
    for a in activities:
        try:
            ts_str = a.get("startTimeGMT")
            if not ts_str:
                continue

            sport = (a.get("activityType") or {}).get("typeKey", "unknown")
            p = Point("activity").tag("sport", sport).time(_parse_gmt(ts_str))

            # Use is not None check to handle activityId=0 correctly
            fields: dict[str, Any] = {
                "activity_id": float(a["activityId"]) if a.get("activityId") is not None else None,
                "distance_m": _fval(a, "distance"),
                "duration_s": _fval(a, "duration"),
                "avg_hr_bpm": _fval(a, "averageHR"),
                "max_hr_bpm": _fval(a, "maxHR"),
                "calories_kcal": _fval(a, "calories"),
                "elevation_gain_m": _fval(a, "elevationGain"),
                "avg_speed_m_s": _fval(a, "avgSpeed"),
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
                        "vertical_oscillation_mm": _fval(a, "avgVerticalOscillation"),
                        "stride_length_mm": _fval(a, "avgStrideLength"),
                        "vertical_ratio_pct": _fval(a, "avgVerticalRatio"),
                        "avg_power_w": _fval(a, "avgPower"),
                    }
                )

            p, n = _add_fields(p, fields)
            if n:
                points.append(p)
        except (GarminConnectAuthenticationError, GarminConnectTooManyRequestsError):
            raise
        except Exception as exc:
            log.warning("activity %s: %s", a.get("activityId"), exc)

    _write(client, points)
    _advance_state(state, "activities", points, end)
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
        except (GarminConnectAuthenticationError, GarminConnectTooManyRequestsError):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("daily_stats %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = (_first_err - timedelta(days=1)) if _first_err else end
    _write(client, points)
    _advance_state(state, "daily_stats", points, watermark)
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
                # sleepScores can be a dict or a list; normalise to dict
                _ss = raw.get("sleepScores")
                scores: dict[str, Any] = (
                    (_ss[0] if _ss else {}) if isinstance(_ss, list) else (_ss or {})
                )
                p = Point("sleep").time(_day_ts(d))
                fields = {
                    "total_sleep_s": _fval(daily, "sleepTimeSeconds"),
                    "deep_sleep_s": _fval(daily, "deepSleepSeconds"),
                    "light_sleep_s": _fval(daily, "lightSleepSeconds"),
                    "rem_sleep_s": _fval(daily, "remSleepSeconds"),
                    "awake_s": _fval(daily, "awakeSleepSeconds"),
                    "sleep_score": _fval(scores, "overall", "value"),
                    "avg_hrv_ms": _fval(daily, "avgOvernightHrv"),
                    "avg_spo2_pct": _fval(daily, "averageSpO2Value"),
                    "avg_breathing_rate": _fval(daily, "averageRespirationValue"),
                    "avg_stress": _fval(daily, "avgSleepStress"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (GarminConnectAuthenticationError, GarminConnectTooManyRequestsError):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("sleep %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = (_first_err - timedelta(days=1)) if _first_err else end
    _write(client, points)
    _advance_state(state, "sleep", points, watermark)
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
                    "last_night_ms": _fval(summary, "lastNight"),
                    "last_5min_high_ms": _fval(summary, "lastNight5MinHigh"),
                    "status": status_num.get(str(summary.get("status", "")), None),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (GarminConnectAuthenticationError, GarminConnectTooManyRequestsError):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("hrv %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = (_first_err - timedelta(days=1)) if _first_err else end
    _write(client, points)
    _advance_state(state, "hrv", points, watermark)
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
                    "hrv_status": _fval(item, "hrvStatus"),
                    "sleep_score": _fval(item, "sleepScore"),
                    "recovery_time_h": _fval(item, "recoveryTime"),
                    "acw_ratio": _fval(item, "acwRatio"),
                }
                p, n = _add_fields(p, fields)
                if n:
                    points.append(p)
        except (GarminConnectAuthenticationError, GarminConnectTooManyRequestsError):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("training_readiness %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = (_first_err - timedelta(days=1)) if _first_err else end
    _write(client, points)
    _advance_state(state, "training_readiness", points, watermark)
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
        except (GarminConnectAuthenticationError, GarminConnectTooManyRequestsError):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("training_status %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = (_first_err - timedelta(days=1)) if _first_err else end
    _write(client, points)
    _advance_state(state, "training_status", points, watermark)
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
        except (GarminConnectAuthenticationError, GarminConnectTooManyRequestsError):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("performance %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = (_first_err - timedelta(days=1)) if _first_err else end
    _write(client, points)
    _advance_state(state, "performance", points, watermark)
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
        fields = {
            "lt_hr_bpm": _fval(lt, "heartRateThreshold"),
            "lt_pace_s_per_km": _fval(lt, "paceThreshold"),
        }
        p, n = _add_fields(p, fields)
        if n:
            _write(client, [p])
            state["lactate_threshold"] = test_date.isoformat()
            _save_state(state)
            log.info("lactate_threshold: wrote 1 point at %s", test_date)
    except (GarminConnectAuthenticationError, GarminConnectTooManyRequestsError):
        raise
    except Exception as exc:
        log.warning("lactate_threshold: %s", exc)


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
        except (GarminConnectAuthenticationError, GarminConnectTooManyRequestsError):
            raise
        except Exception as exc:
            if _first_err is None:
                _first_err = d
            log.warning("respiration %s: %s", d, exc)
        d += timedelta(days=1)
        time.sleep(0.2)

    watermark = (_first_err - timedelta(days=1)) if _first_err else end
    _write(client, points)
    _advance_state(state, "respiration", points, watermark)
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
    sync_lactate_threshold,
    sync_respiration,
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
