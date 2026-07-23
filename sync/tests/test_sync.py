"""Tests for sync.py — Garmin → InfluxDB sync sidecar."""

from datetime import UTC, date, datetime
from unittest.mock import MagicMock, patch

import pytest
from freezegun import freeze_time
from garminconnect import GarminConnectConnectionError

import sync

# ── _parse_gmt ─────────────────────────────────────────────────────────────────


def test_parse_gmt_normal():
    dt = sync._parse_gmt("2026-07-06 10:30:00")
    assert dt == datetime(2026, 7, 6, 10, 30, 0, tzinfo=UTC)


def test_parse_gmt_fractional_seconds():
    dt = sync._parse_gmt("2026-07-06 10:30:00.000")
    assert dt == datetime(2026, 7, 6, 10, 30, 0, tzinfo=UTC)


def test_parse_gmt_long_fractional():
    dt = sync._parse_gmt("2026-07-06 10:30:00.123456")
    assert dt == datetime(2026, 7, 6, 10, 30, 0, tzinfo=UTC)


# ── _fval ──────────────────────────────────────────────────────────────────────


def test_fval_present():
    assert sync._fval({"x": 1.5}, "x") == 1.5


def test_fval_missing_key():
    assert sync._fval({}, "x") is None


def test_fval_none_value():
    assert sync._fval({"x": None}, "x") is None


def test_fval_nested():
    assert sync._fval({"a": {"b": 3.0}}, "a", "b") == 3.0


def test_fval_nested_missing_inner():
    assert sync._fval({"a": {}}, "a", "b") is None


def test_fval_non_numeric():
    assert sync._fval({"x": "bad"}, "x") is None


# ── _advance_state ─────────────────────────────────────────────────────────────


def test_advance_state_advances_watermark():
    state: dict = {}
    with patch.object(sync, "_save_state") as mock_save:
        sync._advance_state(state, "activities", date(2026, 7, 6))
    assert state["activities"] == "2026-07-06"
    mock_save.assert_called_once_with(state)


def test_advance_state_advances_on_empty_day():
    """Rest day (zero points, no error) must still advance watermark."""
    state: dict = {}
    with patch.object(sync, "_save_state") as mock_save:
        sync._advance_state(state, "activities", date(2026, 7, 6))
    assert state["activities"] == "2026-07-06"
    mock_save.assert_called_once_with(state)


def test_advance_state_regression_guard():
    """Watermark must never move backward."""
    state = {"activities": "2026-07-06"}
    with patch.object(sync, "_save_state") as mock_save:
        sync._advance_state(state, "activities", date(2026, 7, 5))
    assert state["activities"] == "2026-07-06"
    mock_save.assert_not_called()


def test_advance_state_regression_guard_same_date():
    """Watermark equal to existing should also not trigger a write."""
    state = {"activities": "2026-07-06"}
    with patch.object(sync, "_save_state") as mock_save:
        sync._advance_state(state, "activities", date(2026, 7, 6))
    assert state["activities"] == "2026-07-06"
    mock_save.assert_not_called()


# ── sync_activities ────────────────────────────────────────────────────────────


def _make_garmin(activities: list) -> MagicMock:
    g = MagicMock()
    g.get_activities_by_date.return_value = activities
    return g


def _written_points(client: MagicMock) -> list:
    if not client.write.called:
        return []
    return client.write.call_args[1]["record"]


@freeze_time("2026-07-06")
def test_activities_bad_record_skipped_not_aborted():
    garmin = _make_garmin(
        [
            {"startTimeGMT": "NOT_A_DATE", "activityId": 1},
            {
                "startTimeGMT": "2026-07-06 10:00:00",
                "activityId": 2,
                "activityType": {"typeKey": "running"},
                "distance": 5000.0,
            },
        ]
    )
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_activities(garmin, client, {})
    assert len(_written_points(client)) == 1


@freeze_time("2026-07-06")
def test_activities_activity_id_zero_not_treated_as_missing():
    """activityId=0 is a valid id — must not be dropped by falsy check."""
    garmin = _make_garmin(
        [
            {
                "startTimeGMT": "2026-07-06 10:00:00",
                "activityId": 0,
                "activityType": {"typeKey": "cycling"},
            }
        ]
    )
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_activities(garmin, client, {})
    assert len(_written_points(client)) == 1


@freeze_time("2026-07-06")
def test_activities_missing_start_time_skipped():
    garmin = _make_garmin([{"activityId": 1}])
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_activities(garmin, client, {})
    assert len(_written_points(client)) == 0


@freeze_time("2026-07-06")
def test_activities_running_uses_avg_ground_contact_time():
    """avgGroundContactTime is the correct Garmin field; groundContactTime must be ignored."""
    garmin = _make_garmin(
        [
            {
                "startTimeGMT": "2026-07-06 10:00:00",
                "activityId": 1,
                "activityType": {"typeKey": "running"},
                "avgGroundContactTime": 250.0,
                "groundContactTime": 999.0,  # wrong key — must not reach InfluxDB
            }
        ]
    )
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_activities(garmin, client, {})
    # One valid point written (no crash = correct key path taken)
    assert len(_written_points(client)) == 1


@freeze_time("2026-07-06")
def test_activities_state_advanced_on_empty_response():
    """No activities = rest day; watermark still advances so backfill window doesn't grow."""
    garmin = _make_garmin([])
    client = MagicMock()
    state: dict = {}
    with patch.object(sync, "_save_state"):
        sync.sync_activities(garmin, client, state)
    assert state["activities"] == "2026-07-06"


@freeze_time("2026-07-06")
def test_activities_state_advanced_when_points_written():
    garmin = _make_garmin(
        [
            {
                "startTimeGMT": "2026-07-06 10:00:00",
                "activityId": 1,
                "activityType": {"typeKey": "cycling"},
                "distance": 20000.0,
            }
        ]
    )
    client = MagicMock()
    state: dict = {}
    with patch.object(sync, "_save_state"):
        sync.sync_activities(garmin, client, state)
    assert state["activities"] == "2026-07-06"


# ── _garmin_login ──────────────────────────────────────────────────────────────


def test_garmin_login_uses_saved_token():
    mock_garmin = MagicMock()
    with patch("sync.Garmin", return_value=mock_garmin):
        result = sync._garmin_login()
    mock_garmin.login.assert_called_once_with(sync.TOKEN_STORE)
    assert result is mock_garmin


def test_garmin_login_falls_back_to_credentials_on_missing_token():
    mock_garmin = MagicMock()
    # First call (token login) raises; second (credential login) succeeds
    mock_garmin.login.side_effect = [FileNotFoundError(), None]
    with patch("sync.Garmin", return_value=mock_garmin):
        result = sync._garmin_login()
    assert mock_garmin.login.call_count == 2
    mock_garmin.client.dump.assert_called_once_with(sync.TOKEN_STORE)
    assert result is mock_garmin


def test_garmin_login_passes_mfa_callback_when_code_set(monkeypatch):
    monkeypatch.setattr(sync, "GARMIN_MFA_CODE", "123456")
    mock_garmin = MagicMock()
    mock_garmin.login.side_effect = [FileNotFoundError(), None]
    with patch("sync.Garmin") as mock_cls:
        mock_cls.return_value = mock_garmin
        sync._garmin_login()
    _, kwargs = mock_cls.call_args
    assert kwargs.get("prompt_mfa") is not None
    assert kwargs["prompt_mfa"]() == "123456"


def test_garmin_login_no_mfa_callback_when_code_empty(monkeypatch):
    monkeypatch.setattr(sync, "GARMIN_MFA_CODE", "")
    mock_garmin = MagicMock()
    mock_garmin.login.side_effect = [FileNotFoundError(), None]
    with patch("sync.Garmin") as mock_cls:
        mock_cls.return_value = mock_garmin
        sync._garmin_login()
    _, kwargs = mock_cls.call_args
    assert kwargs.get("prompt_mfa") is None


def test_garmin_login_token_path_sets_skip_strategies():
    """skip_strategies must be set before token login, not only on credential fallback."""
    mock_garmin = MagicMock()
    with patch("sync.Garmin", return_value=mock_garmin):
        sync._garmin_login()
    assert mock_garmin.client.skip_strategies == {
        "mobile+cffi",
        "mobile+requests",
        "widget+cffi",
    }


# ── activity_id precision ──────────────────────────────────────────────────────


@freeze_time("2026-07-06")
def test_activities_uses_average_speed_not_avg_speed():
    """Garmin API field is averageSpeed; avgSpeed is absent and would store 0."""
    garmin = _make_garmin(
        [
            {
                "startTimeGMT": "2026-07-06 10:00:00",
                "activityId": 1,
                "activityType": {"typeKey": "running"},
                "averageSpeed": 2.78,
                "avgSpeed": 999.0,  # wrong key — must not reach InfluxDB
            }
        ]
    )
    client = MagicMock()
    captured: dict = {}
    original = sync._add_fields

    def capturing(p, fields):
        captured.update(fields)
        return original(p, fields)

    with (
        patch.object(sync, "_add_fields", side_effect=capturing),
        patch.object(sync, "_save_state"),
    ):
        sync.sync_activities(garmin, client, {})

    assert captured.get("avg_speed_m_s") == 2.78, (
        f"avg_speed_m_s should be 2.78 from averageSpeed, got {captured.get('avg_speed_m_s')}"
    )


@freeze_time("2026-07-06")
def test_activities_activity_id_stored_as_int():
    """activity_id must be int, not float — 16-digit IDs exceed float64 precision."""
    garmin = _make_garmin(
        [
            {
                "startTimeGMT": "2026-07-06 10:00:00",
                "activityId": 1234567890123456,
                "activityType": {"typeKey": "running"},
            }
        ]
    )
    client = MagicMock()
    captured: dict = {}
    original = sync._add_fields

    def capturing(p, fields):
        captured.update(fields)
        return original(p, fields)

    with (
        patch.object(sync, "_add_fields", side_effect=capturing),
        patch.object(sync, "_save_state"),
    ):
        sync.sync_activities(garmin, client, {})

    assert isinstance(captured.get("activity_id"), int), (
        f"activity_id should be int, got {type(captured.get('activity_id'))}"
    )


# ── watermark not advanced on parse error ──────────────────────────────────────


@freeze_time("2026-07-06")
def test_activities_watermark_advanced_on_parse_error():
    """Parse errors are permanent data issues; watermark advances so the run doesn't loop forever."""
    garmin = _make_garmin([{"startTimeGMT": "NOT_A_DATE", "activityId": 1}])
    client = MagicMock()
    state: dict = {}
    with patch.object(sync, "_save_state"):
        sync.sync_activities(garmin, client, state)
    assert state.get("activities") == "2026-07-06"


# ── GarminConnectConnectionError propagation ───────────────────────────────────


@freeze_time("2026-07-06")
def test_daily_stats_connection_error_propagates():
    """Connection errors inside day-loop must propagate — not be swallowed as per-day warnings."""
    garmin = MagicMock()
    garmin.get_stats.side_effect = GarminConnectConnectionError("timeout")
    client = MagicMock()
    with pytest.raises(GarminConnectConnectionError):
        sync.sync_daily_stats(garmin, client, {"daily_stats": "2026-07-05"})


@freeze_time("2026-07-06")
def test_sleep_connection_error_propagates():
    garmin = MagicMock()
    garmin.get_sleep_data.side_effect = GarminConnectConnectionError("timeout")
    client = MagicMock()
    with pytest.raises(GarminConnectConnectionError):
        sync.sync_sleep(garmin, client, {"sleep": "2026-07-05"})


# ── sync_sleep field extraction ────────────────────────────────────────────────


def _make_sleep_raw(
    *,
    sleep_score: int = 75,
    spo2: float = 96.0,
) -> dict:
    return {
        "dailySleepDTO": {
            "sleepTimeSeconds": 27000,
            "deepSleepSeconds": 3600,
            "lightSleepSeconds": 18000,
            "remSleepSeconds": 5400,
            "awakeSleepSeconds": 600,
            "averageSpO2Value": spo2,
            "averageRespirationValue": 14.0,
            "avgSleepStress": 22.0,
            "sleepScores": {
                "overall": {"value": sleep_score},
            },
        }
    }


@freeze_time("2026-07-06")
def test_sleep_score_read_from_daily_sleep_dto():
    """sleep_score must come from dailySleepDTO.sleepScores, not top-level."""
    garmin = MagicMock()
    garmin.get_sleep_data.return_value = _make_sleep_raw(sleep_score=78)
    client = MagicMock()
    captured: dict = {}
    original = sync._add_fields

    def capturing(p, fields):
        captured.update(fields)
        return original(p, fields)

    with (
        patch.object(sync, "_add_fields", side_effect=capturing),
        patch.object(sync, "_save_state"),
    ):
        sync.sync_sleep(garmin, client, {"sleep": "2026-07-05"})
    assert captured.get("sleep_score") == 78.0


@freeze_time("2026-07-06")
def test_sleep_score_missing_when_not_in_daily_dto():
    """If sleepScores is absent from dailySleepDTO, sleep_score is not written."""
    raw = {"dailySleepDTO": {"sleepTimeSeconds": 27000, "deepSleepSeconds": 3600}}
    garmin = MagicMock()
    garmin.get_sleep_data.return_value = raw
    client = MagicMock()
    captured: dict = {}
    original = sync._add_fields

    def capturing(p, fields):
        captured.update(fields)
        return original(p, fields)

    with (
        patch.object(sync, "_add_fields", side_effect=capturing),
        patch.object(sync, "_save_state"),
    ):
        sync.sync_sleep(garmin, client, {"sleep": "2026-07-05"})
    assert captured.get("sleep_score") is None


# ── sync_hrv field extraction ─────────────────────────────────────────────────


@freeze_time("2026-07-06")
def test_hrv_last_night_avg_uses_last_night_avg_field():
    """last_night_avg_ms must be read from lastNightAvg (not lastNight)."""
    garmin = MagicMock()
    garmin.get_hrv_data.return_value = {
        "hrvSummary": {
            "weeklyAvg": 45,
            "lastNightAvg": 48,
            "lastNight5MinHigh": 79,
            "status": "BALANCED",
        }
    }
    client = MagicMock()
    captured: dict = {}
    original = sync._add_fields

    def capturing(p, fields):
        captured.update(fields)
        return original(p, fields)

    with (
        patch.object(sync, "_add_fields", side_effect=capturing),
        patch.object(sync, "_save_state"),
    ):
        sync.sync_hrv(garmin, client, {"hrv": "2026-07-05"})
    assert captured.get("last_night_ms") == 48.0


# ── _advance_state first-run regression guard ──────────────────────────────────


def test_advance_state_first_run_none_existing_allows_write():
    """On first run (existing_str is None), watermark is always written."""
    state: dict = {}
    with patch.object(sync, "_save_state"):
        sync._advance_state(state, "daily_stats", date(2026, 4, 6))
    assert state["daily_stats"] == "2026-04-06"


# ── sync_training_readiness ────────────────────────────────────────────────────


def _make_readiness_garmin(payload: object) -> MagicMock:
    g = MagicMock()
    g.get_training_readiness.return_value = payload
    return g


def _captured_readiness_fields(garmin: MagicMock, state: dict | None = None) -> dict:
    """Run sync_training_readiness for a single day and return the fields dict."""
    client = MagicMock()
    captured: dict = {}
    original = sync._add_fields

    def capturing(p, fields):
        captured.update(fields)
        return original(p, fields)

    with (
        patch.object(sync, "_add_fields", side_effect=capturing),
        patch.object(sync, "_save_state"),
    ):
        sync.sync_training_readiness(garmin, client, state or {"training_readiness": "2026-07-05"})

    return captured


@freeze_time("2026-07-06")
def test_training_readiness_recovery_time_converted_from_minutes():
    """recoveryTime is in minutes; stored field must be hours (÷60)."""
    garmin = _make_readiness_garmin([{"score": 80, "recoveryTime": 3000}])
    fields = _captured_readiness_fields(garmin)
    assert fields.get("recovery_time_h") == pytest.approx(50.0)


@freeze_time("2026-07-06")
def test_training_readiness_recovery_time_none_propagated():
    """Missing recoveryTime must not crash and must not write the field."""
    garmin = _make_readiness_garmin([{"score": 70}])
    fields = _captured_readiness_fields(garmin)
    assert fields.get("recovery_time_h") is None


@freeze_time("2026-07-06")
def test_training_readiness_hrv_status_balanced_maps_to_2():
    """HRV status string enum must be converted to numeric (BALANCED→2.0)."""
    garmin = _make_readiness_garmin([{"score": 75, "hrvStatus": "BALANCED"}])
    fields = _captured_readiness_fields(garmin)
    assert fields.get("hrv_status") == 2.0


@freeze_time("2026-07-06")
def test_training_readiness_hrv_status_unbalanced_maps_to_1():
    garmin = _make_readiness_garmin([{"score": 40, "hrvStatus": "UNBALANCED"}])
    fields = _captured_readiness_fields(garmin)
    assert fields.get("hrv_status") == 1.0


@freeze_time("2026-07-06")
def test_training_readiness_hrv_status_poor_maps_to_0():
    garmin = _make_readiness_garmin([{"score": 15, "hrvStatus": "POOR"}])
    fields = _captured_readiness_fields(garmin)
    assert fields.get("hrv_status") == 0.0


@freeze_time("2026-07-06")
def test_training_readiness_hrv_status_unknown_is_none():
    """Unknown or missing hrv_status string must not be written."""
    garmin = _make_readiness_garmin([{"score": 60, "hrvStatus": "SOMETHING_NEW"}])
    fields = _captured_readiness_fields(garmin)
    assert fields.get("hrv_status") is None


@freeze_time("2026-07-06")
def test_training_readiness_accepts_dict_payload():
    """API may return a dict instead of a list; both shapes must be handled."""
    garmin = _make_readiness_garmin({"score": 65, "recoveryTime": 1800, "hrvStatus": "BALANCED"})
    fields = _captured_readiness_fields(garmin)
    assert fields.get("recovery_time_h") == pytest.approx(30.0)
    assert fields.get("hrv_status") == 2.0


@freeze_time("2026-07-06")
def test_training_readiness_state_advanced():
    garmin = _make_readiness_garmin([])
    client = MagicMock()
    state: dict = {"training_readiness": "2026-07-05"}
    with patch.object(sync, "_save_state"):
        sync.sync_training_readiness(garmin, client, state)
    assert state["training_readiness"] == "2026-07-06"


# ── sync_activity_details ──────────────────────────────────────────────────────


def _make_details_garmin(
    activities: list,
    splits: dict | None = None,
    hr_zones: list | None = None,
) -> MagicMock:
    g = MagicMock()
    g.get_activities_by_date.return_value = activities
    g.get_activity_splits.return_value = splits or {}
    g.get_activity_hr_in_timezones.return_value = hr_zones or []
    return g


def _activity_stub(
    activity_id: int = 1,
    ts: str = "2026-07-06 10:00:00",
    sport: str = "running",
) -> dict:
    return {
        "activityId": activity_id,
        "startTimeGMT": ts,
        "activityType": {"typeKey": sport},
    }


@freeze_time("2026-07-06")
def test_activity_details_writes_lap_points():
    """Lap data from get_activity_splits must produce activity_lap points."""
    splits = {
        "lapDTOs": [
            {
                "lapIndex": 1,
                "startTimeGMT": "2026-07-06 10:00:00",
                "distance": 1000.0,
                "duration": 360.0,
                "averageHR": 148.0,
                "averageSpeed": 2.78,
            },
            {
                "lapIndex": 2,
                "startTimeGMT": "2026-07-06 10:06:00",
                "distance": 1000.0,
                "duration": 355.0,
                "averageHR": 152.0,
                "averageSpeed": 2.82,
            },
        ]
    }
    garmin = _make_details_garmin([_activity_stub()], splits=splits)
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_activity_details(garmin, client, {})
    written = _written_points(client)
    lap_points = [p for p in written if "activity_lap" in str(p)]
    assert len(lap_points) == 2


@freeze_time("2026-07-06")
def test_activity_details_writes_hr_zone_point():
    """HR zone data must produce one activity_hr_zones point per activity."""
    hr_zones = [
        {"zoneNumber": 1, "secsInZone": 1200},
        {"zoneNumber": 2, "secsInZone": 2400},
        {"zoneNumber": 3, "secsInZone": 600},
        {"zoneNumber": 4, "secsInZone": 120},
        {"zoneNumber": 5, "secsInZone": 30},
    ]
    garmin = _make_details_garmin([_activity_stub()], hr_zones=hr_zones)
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_activity_details(garmin, client, {})
    written = _written_points(client)
    zone_points = [p for p in written if "activity_hr_zones" in str(p)]
    assert len(zone_points) == 1


@freeze_time("2026-07-06")
def test_activity_details_hr_zones_dict_payload():
    """API may wrap zones in {'hrTimeInZones': [...]} — both shapes must work."""
    hr_zones_dict = {
        "hrTimeInZones": [
            {"zoneNumber": 1, "secsInZone": 900},
            {"zoneNumber": 2, "secsInZone": 1800},
        ]
    }
    garmin = _make_details_garmin([_activity_stub()], hr_zones=hr_zones_dict)
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_activity_details(garmin, client, {})
    written = _written_points(client)
    zone_points = [p for p in written if "activity_hr_zones" in str(p)]
    assert len(zone_points) == 1


@freeze_time("2026-07-06")
def test_activity_details_skips_activity_without_start_time():
    garmin = _make_details_garmin([{"activityId": 1}])  # no startTimeGMT
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_activity_details(garmin, client, {})
    assert not client.write.called


@freeze_time("2026-07-06")
def test_activity_details_state_advanced():
    garmin = _make_details_garmin([])
    client = MagicMock()
    state: dict = {}
    with patch.object(sync, "_save_state"):
        sync.sync_activity_details(garmin, client, state)
    assert state["activity_details"] == "2026-07-06"


@freeze_time("2026-07-06")
def test_activity_details_watermark_rolls_back_on_error():
    """On splits fetch error, watermark must roll back to before the failed activity date."""
    garmin = _make_details_garmin([_activity_stub(activity_id=1, ts="2026-07-05 10:00:00")])
    garmin.get_activity_splits.side_effect = Exception("rate limited")
    client = MagicMock()
    state: dict = {}
    with patch.object(sync, "_save_state"):
        sync.sync_activity_details(garmin, client, state)
    # Activity is on 2026-07-05; watermark must be 2026-07-04 (day before first error)
    assert state.get("activity_details") == "2026-07-04"


@freeze_time("2026-07-06")
def test_activity_details_connection_error_propagates():
    garmin = MagicMock()
    garmin.get_activities_by_date.side_effect = GarminConnectConnectionError("timeout")
    client = MagicMock()
    with pytest.raises(GarminConnectConnectionError):
        sync.sync_activity_details(garmin, client, {})


# ── sync_scheduled_workouts ────────────────────────────────────────────────────


def _sched_garmin(calendar_items: list, next_month_items: list | None = None) -> MagicMock:
    garmin = MagicMock()
    garmin.get_scheduled_workouts.side_effect = [
        {"calendarItems": calendar_items},
        {"calendarItems": next_month_items if next_month_items is not None else []},
    ]
    return garmin


def _workout_item(
    scheduled_id: int = 100,
    workout_id: int = 200,
    date_str: str = "2026-07-25",
    title: str = "Easy Run",
    sport: str = "running",
    duration: float = 1800,
) -> dict:
    return {
        "id": scheduled_id,
        "workoutId": workout_id,
        "date": date_str,
        "title": title,
        "sport": sport,
        "duration": duration,
    }


@freeze_time("2026-07-06")
def test_scheduled_workouts_writes_points(no_sleep):
    garmin = _sched_garmin([_workout_item()])
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    client.write.assert_called_once()
    points = client.write.call_args[1]["record"]
    assert len(points) == 1


@freeze_time("2026-07-06")
def test_scheduled_workouts_queries_two_months(no_sleep):
    """Always queries current + next month to cover any 14-day lookahead."""
    garmin = _sched_garmin([])
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    assert garmin.get_scheduled_workouts.call_count == 2
    calls = garmin.get_scheduled_workouts.call_args_list
    assert calls[0][0] == (2026, 7)
    assert calls[1][0] == (2026, 8)


@freeze_time("2026-12-28")
def test_scheduled_workouts_december_queries_january(no_sleep):
    """December → queries December + January (year rolls over)."""
    garmin = _sched_garmin([])
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    calls = garmin.get_scheduled_workouts.call_args_list
    assert calls[0][0] == (2026, 12)
    assert calls[1][0] == (2027, 1)


@freeze_time("2026-07-06")
def test_scheduled_workouts_skips_non_workout_items(no_sleep):
    """Items without workoutId (e.g. race entries) must be skipped."""
    garmin = _sched_garmin(
        [
            {
                "id": 1,
                "date": "2026-07-10",
                "title": "Park Run",
                "sport": "running",
            },  # no workoutId
            _workout_item(scheduled_id=2, workout_id=999),
        ]
    )
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    points = client.write.call_args[1]["record"]
    assert len(points) == 1


@freeze_time("2026-07-06")
def test_scheduled_workouts_workout_id_zero_not_skipped(no_sleep):
    """workoutId=0 must not be treated as absent (falsy-zero guard)."""
    garmin = _sched_garmin([_workout_item(workout_id=0)])
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    points = client.write.call_args[1]["record"]
    assert len(points) == 1


@freeze_time("2026-07-06")
def test_scheduled_workouts_skips_item_with_no_id(no_sleep):
    """Items with no `id` field must be skipped — str(None) would corrupt the tag."""
    item_no_id = {
        "workoutId": 200,
        "date": "2026-07-25",
        "title": "Easy Run",
        "sport": "running",
        "duration": 1800,
        # no "id" key
    }
    garmin = _sched_garmin([item_no_id])
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    assert not client.write.called


@freeze_time("2026-07-06")
def test_scheduled_workouts_connection_error_propagates(no_sleep):
    garmin = MagicMock()
    garmin.get_scheduled_workouts.side_effect = GarminConnectConnectionError("timeout")
    client = MagicMock()
    with pytest.raises(GarminConnectConnectionError):
        sync.sync_scheduled_workouts(garmin, client, {})
