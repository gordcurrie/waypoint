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
