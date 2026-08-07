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


# ── _login_and_wrap (DRIFT_CHECK_ENABLED gate) ───────────────────────────────────


def test_login_and_wrap_skips_drift_check_by_default(monkeypatch):
    monkeypatch.setattr(sync, "DRIFT_CHECK_ENABLED", False)
    mock_garmin = MagicMock()
    with (
        patch.object(sync, "_garmin_login", return_value=mock_garmin),
        patch.object(sync.drift_check, "wrap") as mock_wrap,
    ):
        result = sync._login_and_wrap()
    mock_wrap.assert_not_called()
    assert result is mock_garmin


def test_login_and_wrap_wraps_when_enabled(monkeypatch):
    monkeypatch.setattr(sync, "DRIFT_CHECK_ENABLED", True)
    mock_garmin = MagicMock()
    with (
        patch.object(sync, "_garmin_login", return_value=mock_garmin),
        patch.object(sync.drift_check, "wrap") as mock_wrap,
    ):
        result = sync._login_and_wrap()
    mock_wrap.assert_called_once_with(mock_garmin)
    assert result is mock_wrap.return_value


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
def test_training_readiness_no_hrv_status_field():
    """hrv_status was removed (bug #58: hrvStatus doesn't exist on this endpoint —
    the BALANCED/UNBALANCED/POOR enum actually lives in get_hrv_data, already synced
    separately as hrv.status). A stray hrvStatus key must not resurrect the field."""
    garmin = _make_readiness_garmin([{"score": 75, "hrvStatus": "BALANCED"}])
    fields = _captured_readiness_fields(garmin)
    assert "hrv_status" not in fields


@freeze_time("2026-07-06")
def test_training_readiness_accepts_dict_payload():
    """API may return a dict instead of a list; both shapes must be handled."""
    garmin = _make_readiness_garmin({"score": 65, "recoveryTime": 1800})
    fields = _captured_readiness_fields(garmin)
    assert fields.get("recovery_time_h") == pytest.approx(30.0)


@freeze_time("2026-07-06")
def test_training_readiness_state_advanced():
    garmin = _make_readiness_garmin([])
    client = MagicMock()
    state: dict = {"training_readiness": "2026-07-05"}
    with patch.object(sync, "_save_state"):
        sync.sync_training_readiness(garmin, client, state)
    assert state["training_readiness"] == "2026-07-06"


# ── sync_training_status ──────────────────────────────────────────────────────

_TRAINING_STATUS_API_RESPONSE = {
    "mostRecentVO2Max": {
        "generic": {"vo2MaxPreciseValue": 47.0, "fitnessAge": None},
        "cycling": None,
    },
    "mostRecentTrainingStatus": {
        "latestTrainingStatusData": {
            "3620139022": {
                "trainingStatus": 7,
                "trainingStatusFeedbackPhrase": "PRODUCTIVE_2",
                "primaryTrainingDevice": True,
            }
        }
    },
}


def _make_status_garmin(payload: object) -> MagicMock:
    g = MagicMock()
    g.get_training_status.return_value = payload
    return g


def _captured_status_fields(garmin: MagicMock, state: dict | None = None) -> dict:
    client = MagicMock()
    captured: dict = {}
    original = sync._add_fields

    def capturing(p, fields):
        captured.update(fields)
        return original(p, fields)

    with (
        patch.object(sync, "_add_fields", side_effect=capturing),
        patch.object(sync, "_save_state"),
        patch.object(sync.time, "sleep"),
    ):
        sync.sync_training_status(garmin, client, state or {"training_status": "2026-07-05"})

    return {k: v for k, v in captured.items() if v is not None}


@freeze_time("2026-07-06")
def test_training_status_productive_maps_to_3():
    """PRODUCTIVE_2 phrase must map to status_num=3.0."""
    garmin = _make_status_garmin(_TRAINING_STATUS_API_RESPONSE)
    fields = _captured_status_fields(garmin)
    assert fields.get("status_num") == 3.0


@freeze_time("2026-07-06")
def test_training_status_vo2max_extracted_from_nested_path():
    """vo2max_running must come from mostRecentVO2Max.generic.vo2MaxPreciseValue."""
    garmin = _make_status_garmin(_TRAINING_STATUS_API_RESPONSE)
    fields = _captured_status_fields(garmin)
    assert fields.get("vo2max_running") == pytest.approx(47.0)


@freeze_time("2026-07-06")
def test_training_status_cycling_vo2max_none_when_absent():
    """cycling vo2max is null in API; must not write the field."""
    garmin = _make_status_garmin(_TRAINING_STATUS_API_RESPONSE)
    fields = _captured_status_fields(garmin)
    assert "vo2max_cycling" not in fields


@freeze_time("2026-07-06")
def test_training_status_prefers_primary_device():
    """When multiple devices exist, must pick primaryTrainingDevice=True."""
    payload = {
        "mostRecentVO2Max": {
            "generic": {"vo2MaxPreciseValue": 47.0, "fitnessAge": None},
            "cycling": None,
        },
        "mostRecentTrainingStatus": {
            "latestTrainingStatusData": {
                "9999": {
                    "trainingStatus": 8,
                    "trainingStatusFeedbackPhrase": "PEAKING_1",
                    "primaryTrainingDevice": False,
                },
                "3620139022": {
                    "trainingStatus": 7,
                    "trainingStatusFeedbackPhrase": "PRODUCTIVE_2",
                    "primaryTrainingDevice": True,
                },
            }
        },
    }
    garmin = _make_status_garmin(payload)
    fields = _captured_status_fields(garmin)
    assert fields.get("status_num") == 3.0


@freeze_time("2026-07-06")
def test_training_status_does_not_write_fitness_age():
    """fitness_age was removed from training_status (bug found 2026-08-02) — real data
    comes from a separate endpoint, consumed by sync_performance instead. The
    mostRecentVO2Max.generic.fitnessAge path here always reads null on this account."""
    garmin = _make_status_garmin(_TRAINING_STATUS_API_RESPONSE)
    fields = _captured_status_fields(garmin)
    assert "fitness_age" not in fields


@freeze_time("2026-07-06")
def test_training_status_no_device_data_writes_nothing():
    """Empty latestTrainingStatusData must not write any point."""
    payload = {
        "mostRecentTrainingStatus": {"latestTrainingStatusData": {}},
        "mostRecentVO2Max": None,
    }
    garmin = _make_status_garmin(payload)
    client = MagicMock()
    with patch.object(sync, "_save_state"), patch.object(sync.time, "sleep"):
        sync.sync_training_status(garmin, client, {"training_status": "2026-07-05"})
    client.write.assert_not_called()


@freeze_time("2026-07-06")
def test_training_status_unknown_phrase_status_num_not_written():
    """Unrecognised feedback phrase must not write status_num (not crash)."""
    payload = {
        "mostRecentVO2Max": {
            "generic": {"vo2MaxPreciseValue": 47.0, "fitnessAge": None},
            "cycling": None,
        },
        "mostRecentTrainingStatus": {
            "latestTrainingStatusData": {
                "3620139022": {
                    "trainingStatusFeedbackPhrase": "UNKNOWN_STATUS_99",
                    "primaryTrainingDevice": True,
                }
            }
        },
    }
    garmin = _make_status_garmin(payload)
    fields = _captured_status_fields(garmin)
    assert "status_num" not in fields


# ── sync_performance ───────────────────────────────────────────────────────────


def _make_performance_garmin(max_metrics_payload: object, fitness_age_payload: object) -> MagicMock:
    g = MagicMock()
    g.get_max_metrics.return_value = max_metrics_payload
    g.connectapi.return_value = fitness_age_payload
    return g


def _captured_performance_fields(garmin: MagicMock, state: dict | None = None) -> dict:
    client = MagicMock()
    captured: dict = {}
    original = sync._add_fields

    def capturing(p, fields):
        captured.update(fields)
        return original(p, fields)

    with (
        patch.object(sync, "_add_fields", side_effect=capturing),
        patch.object(sync, "_save_state"),
        patch.object(sync.time, "sleep"),
    ):
        sync.sync_performance(garmin, client, state or {"performance": "2026-07-05"})

    return {k: v for k, v in captured.items() if v is not None}


@freeze_time("2026-07-06")
def test_performance_fitness_age_from_dedicated_endpoint_not_generic_vo2max():
    """fitness_age must come from fitnessage-service/fitnessage/<date> (bug found
    2026-08-02) — the old mostRecentVO2Max.generic.fitnessAge path always reads
    null on this account, confirmed live against the Garmin Connect UI's Fitness
    Age page."""
    garmin = _make_performance_garmin(
        max_metrics_payload=[{"generic": {"vo2MaxPreciseValue": 46.6, "fitnessAge": None}}],
        fitness_age_payload={"chronologicalAge": 50, "fitnessAge": 44.74},
    )
    fields = _captured_performance_fields(garmin)
    assert fields.get("vo2max") == pytest.approx(46.6)
    assert fields.get("fitness_age") == pytest.approx(44.74)


@freeze_time("2026-07-06")
def test_performance_fitness_age_key_absent_on_stale_data():
    """On stale rolling-average data, the fitnessAge key is absent entirely
    (not null) — must not crash, must write no fitness_age field."""
    garmin = _make_performance_garmin(
        max_metrics_payload=[{"generic": {"vo2MaxPreciseValue": 46.6}}],
        fitness_age_payload={"chronologicalAge": 50, "components": {}},
    )
    fields = _captured_performance_fields(garmin)
    assert fields.get("vo2max") == pytest.approx(46.6)
    assert "fitness_age" not in fields


@freeze_time("2026-07-06")
def test_performance_writes_point_from_fitness_age_alone():
    """A day with no VO2max update but a fitness_age value must still write a point —
    the two metrics update independently."""
    garmin = _make_performance_garmin(
        max_metrics_payload=[],
        fitness_age_payload={"chronologicalAge": 50, "fitnessAge": 44.74},
    )
    client = MagicMock()
    with (
        patch.object(sync, "_save_state"),
        patch.object(sync.time, "sleep"),
    ):
        sync.sync_performance(garmin, client, {"performance": "2026-07-05"})
    assert client.write.called


@freeze_time("2026-07-06")
def test_performance_fitness_age_failure_does_not_drop_vo2max():
    """A fitness_age-specific fetch failure must not also drop that day's vo2max
    (bug flagged in code review on #70) — the two are fetched independently."""
    garmin = _make_performance_garmin(
        max_metrics_payload=[{"generic": {"vo2MaxPreciseValue": 46.6}}],
        fitness_age_payload=None,
    )
    garmin.connectapi.side_effect = RuntimeError("fitness age endpoint down")
    fields = _captured_performance_fields(garmin)
    assert fields.get("vo2max") == pytest.approx(46.6)
    assert "fitness_age" not in fields


# ── sync_lactate_threshold ──────────────────────────────────────────────────────


def _make_lt_garmin(payload: object) -> MagicMock:
    g = MagicMock()
    g.get_lactate_threshold.return_value = payload
    return g


@freeze_time("2026-07-06")
def test_lactate_threshold_reads_nested_speed_and_heart_rate():
    """HR/pace/date all live under speed_and_heart_rate (bug #56) — there is no
    top-level heartRateThreshold/paceThreshold/testDate."""
    garmin = _make_lt_garmin(
        {
            "speed_and_heart_rate": {
                "calendarDate": "2026-07-06T11:42:53.885",
                "speed": 0.5,
                "heartRate": 165,
            }
        }
    )
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_lactate_threshold(garmin, client, {})
    points = _written_points(client)
    assert len(points) == 1
    assert "lt_hr_bpm=165" in str(points[0])
    assert "lt_pace_s_per_km=200" in str(points[0])  # 100.0 / 0.5


@freeze_time("2026-07-06")
def test_lactate_threshold_speed_is_scaled_by_ten():
    """speed is 1/10th of true m/s — pace is 100/speed, not 1000/speed or speed*1000 (bug #56).

    Regression value verified 2026-07-30 against connect.garmin.com's Lactate
    Threshold report's chart tooltips at 4 separate dates: raw speed 0.33888794
    corresponds to a displayed 165bpm / 4:55/km (295 s/km) / 369W threshold.
    1000.0/speed (this bug's first, still-wrong fix) gives 2950.8 — 10x too slow.
    """
    garmin = _make_lt_garmin({"speed_and_heart_rate": {"speed": 0.33888794, "heartRate": 165}})
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_lactate_threshold(garmin, client, {})
    points = _written_points(client)
    # Read the field value directly rather than parsing Point's string repr —
    # sidesteps any dependence on its float formatting (Copilot #65/#66).
    assert points[0]._fields["lt_pace_s_per_km"] == pytest.approx(100.0 / 0.33888794)  # ~= 4:55/km


@freeze_time("2026-07-06")
def test_lactate_threshold_missing_response_writes_nothing():
    garmin = _make_lt_garmin(None)
    client = MagicMock()
    sync.sync_lactate_threshold(garmin, client, {})
    assert not client.write.called


@freeze_time("2026-07-06")
def test_lactate_threshold_skips_rewrite_of_same_test_date():
    garmin = _make_lt_garmin(
        {"speed_and_heart_rate": {"calendarDate": "2026-07-06", "speed": 0.5, "heartRate": 165}}
    )
    client = MagicMock()
    state = {"lactate_threshold": "2026-07-06"}
    sync.sync_lactate_threshold(garmin, client, state)
    assert not client.write.called


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
def test_activity_details_lap_uses_real_power_and_elevation_keys():
    """Lap power/elevation must come from averagePower/elevationGain — the real keys.

    avgPower/totalAscent don't exist on a real lap object (bug #59); asserting the
    real values are present and the wrong-key values are absent guards against
    reintroducing either typo.
    """
    splits = {
        "lapDTOs": [
            {
                "lapIndex": 1,
                "startTimeGMT": "2026-07-06 10:00:00",
                "distance": 1000.0,
                "duration": 360.0,
                "averagePower": 250.0,
                "elevationGain": 12.0,
                "avgPower": 999.0,
                "totalAscent": 999.0,
            },
        ]
    }
    garmin = _make_details_garmin([_activity_stub()], splits=splits)
    client = MagicMock()
    with patch.object(sync, "_save_state"):
        sync.sync_activity_details(garmin, client, {})
    written = _written_points(client)
    lap_point = next(p for p in written if "activity_lap" in str(p))
    assert "avg_power_w=250" in str(lap_point)
    assert "elevation_gain_m=12" in str(lap_point)
    assert "avg_power_w=999" not in str(lap_point)
    assert "elevation_gain_m=999" not in str(lap_point)


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
        "sportTypeKey": sport,
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
def test_scheduled_workouts_sport_tag_uses_sport_type_key(no_sleep):
    """sport tag must come from sportTypeKey — the only sport field real calendar items have."""
    garmin = _sched_garmin([_workout_item(sport="cycling")])
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    points = client.write.call_args[1]["record"]
    assert "sport=cycling" in str(points[0])


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


def _coach_plan_item(
    scheduled_id: int = 300,
    training_plan_id: int = 46457367,
    date_str: str = "2026-08-05",
    title: str = "Tempo",
    sport: str = "running",
) -> dict:
    """A coach/training-plan-assigned item as returned live 2026-08-02 — itemType
    fbtAdaptiveWorkout, workoutId always null, real identifier is trainingPlanId."""
    return {
        "id": scheduled_id,
        "workoutId": None,
        "trainingPlanId": training_plan_id,
        "itemType": "fbtAdaptiveWorkout",
        "date": date_str,
        "title": title,
        "sportTypeKey": sport,
        "duration": None,
    }


@freeze_time("2026-07-06")
def test_scheduled_workouts_includes_coach_plan_items(no_sleep):
    """fbtAdaptiveWorkout items with a trainingPlanId are real coach-assigned workouts,
    not the auto-generated suggestions they were previously assumed to be — must be
    synced even though workoutId is null (bug found 2026-08-02: calendar showed nothing
    despite the coach having added a week of workouts)."""
    garmin = _sched_garmin([_coach_plan_item()])
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    client.write.assert_called_once()
    points = client.write.call_args[1]["record"]
    assert len(points) == 1
    assert 'name="Tempo"' in str(points[0])
    assert "workout_id=" not in str(points[0])


@freeze_time("2026-07-06")
def test_scheduled_workouts_coach_plan_regeneration_dedupes(no_sleep):
    """Coach-plan items are tagged by (sport, workout_name), not the calendar item's
    own id — that id churns every time the adaptive plan regenerates the day (same
    real workout, brand-new id), which previously piled up ghost duplicates in
    InfluxDB (no DELETE support). A resync of the "same" day with a new id must
    write a point with the same tags (verified by absence of the old scheduled_id
    tag), not a distinguishable new one."""
    garmin = _sched_garmin([_coach_plan_item(scheduled_id=111, title="Base")])
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    first = str(client.write.call_args[1]["record"][0])
    assert "scheduled_id=" not in first
    assert "sport=running" in first
    assert "workout_name=Base" in first

    # Same logical day, regenerated: id changes, title/sport stay the same.
    garmin2 = _sched_garmin([_coach_plan_item(scheduled_id=999, title="Base")])
    client2 = MagicMock()
    sync.sync_scheduled_workouts(garmin2, client2, {})
    second = str(client2.write.call_args[1]["record"][0])
    assert "scheduled_id=" not in second
    assert "sport=running" in second
    assert "workout_name=Base" in second


@freeze_time("2026-07-06")
def test_scheduled_workouts_coach_two_a_day_kept_separate(no_sleep):
    """A real coach-assigned two-a-day (e.g. a run + a strength session on the same
    date) must produce two distinct points, distinguished by sport — verified live
    2026-08-07 against an actual run + strength_training coach day."""
    garmin = _sched_garmin(
        [
            _coach_plan_item(scheduled_id=1, date_str="2026-07-10", title="Base", sport="running"),
            _coach_plan_item(
                scheduled_id=2,
                date_str="2026-07-10",
                title="Total Body Circuit",
                sport="strength_training",
            ),
        ]
    )
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    points = [str(p) for p in client.write.call_args[1]["record"]]
    assert len(points) == 2
    assert any("sport=running" in p and "workout_name=Base" in p for p in points)
    assert any(
        "sport=strength_training" in p and "workout_name=Total\\ Body\\ Circuit" in p
        for p in points
    )


@freeze_time("2026-07-06")
def test_scheduled_workouts_self_created_keeps_scheduled_id_tag(no_sleep):
    """Self-created workouts (real, stable workoutId) still dedupe/identify by their
    own scheduled_id — only coach-plan items switch to (sport, workout_name)."""
    garmin = _sched_garmin([_workout_item(scheduled_id=42, workout_id=999)])
    client = MagicMock()
    sync.sync_scheduled_workouts(garmin, client, {})
    points = str(client.write.call_args[1]["record"][0])
    assert "scheduled_id=42" in points
    assert "workout_name=" not in points


@freeze_time("2026-07-06")
def test_scheduled_workouts_fbt_adaptive_without_training_plan_id_skipped(no_sleep):
    """fbtAdaptiveWorkout with no trainingPlanId is not a confirmed real workout — stay
    conservative and skip rather than guess."""
    item = _coach_plan_item()
    item["trainingPlanId"] = None
    garmin = _sched_garmin([item])
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


# ── sync_training_plan ──────────────────────────────────────────────────────────


def _plan_task(
    calendar_date: str,
    workout_name: str | None = "Base",
    description: str | None = "137bpm",
    duration: float | None = 1800,
    distance: float | None = 5000,
    rest_day: bool = False,
    workout_phrase: str | None = "BASE",
    sport: str | None = "running",
) -> dict:
    return {
        "calendarDate": calendar_date,
        "taskWorkout": {
            "workoutId": None,
            "sportType": {"sportTypeKey": sport} if sport else None,
            "workoutName": workout_name,
            "workoutDescription": description,
            "estimatedDurationInSecs": duration,
            "estimatedDistanceInMeters": distance,
            "restDay": rest_day,
            "workoutPhrase": workout_phrase,
        },
    }


def _training_plan_garmin(
    tasks: list,
    plan_id: int = 46457367,
    end_date: str = "2026-10-18T00:00:00.0",
    phases: list | None = None,
) -> MagicMock:
    garmin = MagicMock()
    garmin.get_training_plans.return_value = {
        "trainingPlanList": [{"trainingPlanId": plan_id, "endDate": end_date}]
    }
    garmin.get_adaptive_training_plan_by_id.return_value = {
        "taskList": tasks,
        "adaptivePlanPhases": phases or [],
    }
    return garmin


@freeze_time("2026-08-03")
def test_training_plan_writes_points_within_lookahead(no_sleep):
    """Only tasks within today..+TRAINING_PLAN_LOOKAHEAD_DAYS are synced — the plan
    regenerates day to day, so far-future entries aren't trustworthy yet."""
    garmin = _training_plan_garmin(
        [
            _plan_task("2026-08-03"),
            _plan_task("2026-08-08"),
            _plan_task("2026-09-01"),  # beyond the 14-day lookahead
        ]
    )
    client = MagicMock()
    sync.sync_training_plan(garmin, client, {})
    points = client.write.call_args[1]["record"]
    assert len(points) == 2


@freeze_time("2026-08-03")
def test_training_plan_includes_rest_day(no_sleep):
    """Rest days have no corresponding get_scheduled_workouts calendarItem at all —
    this is the only place they're visible. workoutName is null on a rest day, so
    name falls back to 'Rest'."""
    garmin = _training_plan_garmin(
        [
            _plan_task(
                "2026-08-03",
                workout_name=None,
                description=None,
                duration=None,
                distance=None,
                rest_day=True,
                workout_phrase="TRAINING_READINESS_REST",
            )
        ]
    )
    client = MagicMock()
    sync.sync_training_plan(garmin, client, {})
    points = client.write.call_args[1]["record"]
    assert len(points) == 1
    s = str(points[0])
    assert 'name="Rest"' in s
    assert "rest_day=1" in s


@freeze_time("2026-08-03")
def test_training_plan_two_a_day_kept_separate(no_sleep):
    """A coach day can carry two taskList entries for the same calendarDate (verified
    live 2026-08-07: running + strength_training on the same date) — sport must be
    part of the tag key or the second write silently overwrites the first."""
    garmin = _training_plan_garmin(
        [
            _plan_task("2026-08-03", workout_name="Base", sport="running"),
            _plan_task("2026-08-03", workout_name="Total Body Circuit", sport="strength_training"),
        ]
    )
    client = MagicMock()
    sync.sync_training_plan(garmin, client, {})
    points = [str(p) for p in client.write.call_args[1]["record"]]
    assert len(points) == 2
    assert any("sport=running" in p and 'name="Base"' in p for p in points)
    assert any("sport=strength_training" in p and 'name="Total Body Circuit"' in p for p in points)


@freeze_time("2026-08-03")
def test_training_plan_rest_day_has_empty_sport_tag(no_sleep):
    """Rest days have no sportType at all — must not error, just tag sport as empty."""
    garmin = _training_plan_garmin(
        [_plan_task("2026-08-03", workout_name=None, rest_day=True, sport=None)]
    )
    client = MagicMock()
    sync.sync_training_plan(garmin, client, {})
    points = client.write.call_args[1]["record"]
    assert len(points) == 1
    assert 'name="Rest"' in str(points[0])


@freeze_time("2026-08-03")
def test_training_plan_sets_phase_from_date_range(no_sleep):
    garmin = _training_plan_garmin(
        [_plan_task("2026-08-03")],
        phases=[
            {"startDate": "2026-06-27", "endDate": "2026-08-05", "trainingPhase": "BASE"},
            {"startDate": "2026-08-06", "endDate": "2026-09-13", "trainingPhase": "BUILD"},
        ],
    )
    client = MagicMock()
    sync.sync_training_plan(garmin, client, {})
    points = client.write.call_args[1]["record"]
    assert 'phase="BASE"' in str(points[0])


@freeze_time("2026-08-03")
def test_training_plan_skips_ended_plan(no_sleep):
    garmin = _training_plan_garmin([_plan_task("2026-08-03")], end_date="2026-07-01T00:00:00.0")
    client = MagicMock()
    sync.sync_training_plan(garmin, client, {})
    assert not garmin.get_adaptive_training_plan_by_id.called
    assert not client.write.called


@freeze_time("2026-08-03")
def test_training_plan_skips_plan_with_no_id(no_sleep):
    garmin = MagicMock()
    garmin.get_training_plans.return_value = {"trainingPlanList": [{"endDate": "2026-10-18"}]}
    client = MagicMock()
    sync.sync_training_plan(garmin, client, {})
    assert not garmin.get_adaptive_training_plan_by_id.called
    assert not client.write.called


@freeze_time("2026-08-03")
def test_training_plan_get_training_plans_connection_error_propagates(no_sleep):
    garmin = MagicMock()
    garmin.get_training_plans.side_effect = GarminConnectConnectionError("timeout")
    client = MagicMock()
    with pytest.raises(GarminConnectConnectionError):
        sync.sync_training_plan(garmin, client, {})


@freeze_time("2026-08-03")
def test_training_plan_get_adaptive_plan_connection_error_propagates(no_sleep):
    garmin = _training_plan_garmin([_plan_task("2026-08-03")])
    garmin.get_adaptive_training_plan_by_id.side_effect = GarminConnectConnectionError("timeout")
    client = MagicMock()
    with pytest.raises(GarminConnectConnectionError):
        sync.sync_training_plan(garmin, client, {})


# ── sync_pending_workouts ──────────────────────────────────────────────────────


def _write_queue(tmp_path, items: list) -> None:
    import json as _json

    (tmp_path / "workout_queue.json").write_text(_json.dumps(items))


def _queue_item(
    id: str = "abc123",
    name: str = "Tempo Run",
    sport: str = "running",
    steps: list | None = None,
) -> dict:
    if steps is None:
        steps = [{"type": "interval", "duration_s": 1200, "target_hr_zone": 4}]
    return {"id": id, "name": name, "sport": sport, "steps": steps}


def test_pending_workouts_no_op_when_no_queue_file():
    garmin = MagicMock()
    client = MagicMock()
    sync.sync_pending_workouts(garmin, client, {})
    garmin.upload_workout.assert_not_called()


def test_pending_workouts_no_op_when_queue_empty(tmp_path, monkeypatch):
    monkeypatch.setattr(sync, "DATA_DIR", tmp_path)
    _write_queue(tmp_path, [])
    garmin = MagicMock()
    sync.sync_pending_workouts(garmin, MagicMock(), {})
    garmin.upload_workout.assert_not_called()


def test_pending_workouts_uploads_item_and_clears_queue(tmp_path, monkeypatch):
    monkeypatch.setattr(sync, "DATA_DIR", tmp_path)
    _write_queue(tmp_path, [_queue_item()])
    garmin = MagicMock()
    sync.sync_pending_workouts(garmin, MagicMock(), {})
    garmin.upload_workout.assert_called_once()
    import json as _json

    remaining = _json.loads((tmp_path / "workout_queue.json").read_text())
    assert remaining == []


def test_pending_workouts_keeps_failed_item_in_queue(tmp_path, monkeypatch):
    monkeypatch.setattr(sync, "DATA_DIR", tmp_path)
    _write_queue(tmp_path, [_queue_item(id="fail"), _queue_item(id="ok")])
    garmin = MagicMock()
    garmin.upload_workout.side_effect = [Exception("API error"), None]
    sync.sync_pending_workouts(garmin, MagicMock(), {})
    import json as _json

    remaining = _json.loads((tmp_path / "workout_queue.json").read_text())
    assert len(remaining) == 1
    assert remaining[0]["id"] == "fail"


def _upload_response(
    workout_id: int = 1656732143, name: str = "Tempo Run", sport: str = "running"
) -> dict:
    """Shape confirmed live 2026-08-07 via a probe upload_workout call + delete_workout
    cleanup (see internal/garmin/workout_detail.go's doc comment) — identical to
    get_workout_by_id's response."""
    return {
        "workoutId": workout_id,
        "ownerId": 62914808,
        "workoutName": name,
        "sportType": {"sportTypeId": 1, "sportTypeKey": sport},
        "workoutSegments": [
            {
                "segmentOrder": 1,
                "workoutSteps": [
                    {"type": "ExecutableStepDTO", "stepId": 14222928952, "stepOrder": 1},
                ],
            }
        ],
    }


def test_pending_workouts_writes_workout_detail_on_success(tmp_path, monkeypatch):
    monkeypatch.setattr(sync, "DATA_DIR", tmp_path)
    _write_queue(tmp_path, [_queue_item()])
    garmin = MagicMock()
    garmin.upload_workout.return_value = _upload_response()
    client = MagicMock()
    sync.sync_pending_workouts(garmin, client, {})
    points = _written_points(client)
    assert len(points) == 1
    line = str(points[0])
    assert "workout_detail" in line
    assert "workout_id=1656732143" in line
    assert 'name="Tempo Run"' in line
    assert "stepId" in line


def test_pending_workouts_no_detail_write_when_workout_id_missing(tmp_path, monkeypatch):
    monkeypatch.setattr(sync, "DATA_DIR", tmp_path)
    _write_queue(tmp_path, [_queue_item()])
    garmin = MagicMock()
    garmin.upload_workout.return_value = {"workoutId": None}
    client = MagicMock()
    sync.sync_pending_workouts(garmin, client, {})
    assert not client.write.called


def test_pending_workouts_detail_write_failure_does_not_requeue(tmp_path, monkeypatch):
    """A malformed upload response must not cause the already-uploaded item to be
    treated as failed (which would re-upload and duplicate it on the next run)."""
    monkeypatch.setattr(sync, "DATA_DIR", tmp_path)
    _write_queue(tmp_path, [_queue_item()])
    garmin = MagicMock()
    garmin.upload_workout.return_value = {"workoutId": 123, "workoutSegments": "not-a-list"}
    sync.sync_pending_workouts(garmin, MagicMock(), {})
    import json as _json

    remaining = _json.loads((tmp_path / "workout_queue.json").read_text())
    assert remaining == []


def test_pending_workouts_connection_error_propagates(tmp_path, monkeypatch):
    monkeypatch.setattr(sync, "DATA_DIR", tmp_path)
    _write_queue(tmp_path, [_queue_item()])
    garmin = MagicMock()
    garmin.upload_workout.side_effect = GarminConnectConnectionError("timeout")
    with pytest.raises(GarminConnectConnectionError):
        sync.sync_pending_workouts(garmin, MagicMock(), {})


def test_build_garmin_workout_running_structure():
    item = _queue_item(
        sport="running",
        steps=[
            {"type": "warmup", "duration_s": 600, "description": "easy"},
            {"type": "interval", "duration_s": 1200, "target_hr_zone": 4},
            {"type": "cooldown", "duration_s": 600},
        ],
    )
    w = sync._build_garmin_workout(item)
    assert w["sportType"]["sportTypeKey"] == "running"
    seg = w["workoutSegments"][0]
    assert len(seg["workoutSteps"]) == 3
    assert seg["workoutSteps"][0]["type"] == "ExecutableStepDTO"
    assert seg["workoutSteps"][0]["stepType"]["stepTypeKey"] == "warmup"
    assert seg["workoutSteps"][0]["endConditionValue"] == 600.0
    assert seg["workoutSteps"][1]["type"] == "ExecutableStepDTO"
    assert seg["workoutSteps"][1]["targetType"]["workoutTargetTypeKey"] == "heart.rate.zone"
    assert seg["workoutSteps"][1]["targetValueOne"] == 4.0
    assert seg["workoutSteps"][2]["type"] == "ExecutableStepDTO"
    assert seg["workoutSteps"][2]["targetType"]["workoutTargetTypeKey"] == "no.target"


def test_build_garmin_workout_distance_step():
    item = _queue_item(steps=[{"type": "interval", "distance_m": 1000}])
    w = sync._build_garmin_workout(item)
    step = w["workoutSegments"][0]["workoutSteps"][0]
    assert step["type"] == "ExecutableStepDTO"
    assert step["endCondition"]["conditionTypeKey"] == "distance"
    assert step["endConditionValue"] == 1000.0


def test_build_garmin_workout_unknown_sport_raises():
    item = _queue_item(sport="kayaking")
    with pytest.raises(ValueError, match="unsupported workout sport"):
        sync._build_garmin_workout(item)


def test_build_garmin_workout_swimming_and_strength_ids():
    # Regression test: swimming and strength_training sportTypeIds were previously
    # swapped/wrong (swimming=5 collided with strength_training; strength_training=13
    # resolved to "rucking" on Garmin's backend). Verified live 2026-07-28.
    swim = sync._build_garmin_workout(_queue_item(sport="swimming"))
    assert swim["sportType"] == {"sportTypeId": 4, "sportTypeKey": "swimming"}

    strength = sync._build_garmin_workout(_queue_item(sport="strength_training"))
    assert strength["sportType"] == {"sportTypeId": 5, "sportTypeKey": "strength_training"}


def test_build_garmin_workout_reps_only_flat_step():
    item = _queue_item(steps=[{"type": "interval", "reps": 8}])
    step = sync._build_garmin_workout(item)["workoutSegments"][0]["workoutSteps"][0]
    assert step["type"] == "ExecutableStepDTO"
    assert step["endCondition"]["conditionTypeKey"] == "reps"
    assert step["endConditionValue"] == 8.0
    assert step["childStepId"] is None


def test_build_garmin_workout_category_exercise_name_passthrough():
    item = _queue_item(
        steps=[
            {
                "type": "interval",
                "reps": 8,
                "category": "BENCH_PRESS",
                "exercise_name": "BARBELL_BENCH_PRESS",
            }
        ]
    )
    step = sync._build_garmin_workout(item)["workoutSegments"][0]["workoutSteps"][0]
    assert step["category"] == "BENCH_PRESS"
    assert step["exerciseName"] == "BARBELL_BENCH_PRESS"


def test_build_garmin_workout_category_null_on_plain_steps():
    # Real Garmin workouts serialize category/exerciseName as explicit nulls on
    # non-strength steps, not omitted keys (confirmed against a captured real
    # workout) — match that for round-trip fidelity.
    item = _queue_item(steps=[{"type": "warmup", "duration_s": 300}])
    step = sync._build_garmin_workout(item)["workoutSegments"][0]["workoutSteps"][0]
    assert step["category"] is None
    assert step["exerciseName"] is None


def test_build_garmin_workout_sets_creates_repeat_group():
    item = _queue_item(
        steps=[
            {
                "type": "interval",
                "reps": 8,
                "sets": 3,
                "rest_s": 20,
                "category": "BENCH_PRESS",
                "exercise_name": "BARBELL_BENCH_PRESS",
            }
        ]
    )
    steps = sync._build_garmin_workout(item)["workoutSegments"][0]["workoutSteps"]
    assert len(steps) == 1
    group = steps[0]
    assert group["type"] == "RepeatGroupDTO"
    assert group["stepType"] == {"stepTypeId": 6, "stepTypeKey": "repeat"}
    assert group["numberOfIterations"] == 3
    assert group["endCondition"]["conditionTypeKey"] == "iterations"
    assert group["endConditionValue"] == 3.0
    assert group["stepOrder"] == 1
    assert group["childStepId"] == 1

    exercise_step, rest_step = group["workoutSteps"]
    assert exercise_step["stepOrder"] == 2
    assert exercise_step["childStepId"] == 1
    assert exercise_step["endCondition"]["conditionTypeKey"] == "reps"
    assert exercise_step["endConditionValue"] == 8.0
    assert exercise_step["category"] == "BENCH_PRESS"
    assert exercise_step["exerciseName"] == "BARBELL_BENCH_PRESS"

    assert rest_step["stepOrder"] == 3
    assert rest_step["childStepId"] == 1
    assert rest_step["stepType"] == {"stepTypeId": 5, "stepTypeKey": "rest"}
    assert rest_step["endCondition"]["conditionTypeKey"] == "time"
    assert rest_step["endConditionValue"] == 20.0
    # rest is synthesized, never linked to an exercise
    assert rest_step["category"] is None
    assert rest_step["exerciseName"] is None
    # Real Garmin rest steps serialize targetType as null, not a no.target object
    # (confirmed against the captured fixture) — regression test for PR review.
    assert rest_step["targetType"] is None
    assert exercise_step["targetType"] == {
        "workoutTargetTypeId": 1,
        "workoutTargetTypeKey": "no.target",
    }


def test_build_garmin_workout_invalid_sets_raises():
    # sets=1 is meaningless (create_workout rejects it), but if a malformed item
    # reaches sync.py anyway (hand-edited queue file, a future second producer),
    # it must fail loudly rather than silently building a flat step that drops
    # sets/rest_s entirely.
    item = _queue_item(steps=[{"type": "interval", "reps": 8, "sets": 1, "rest_s": 20}])
    with pytest.raises(ValueError, match="invalid sets/rest_s"):
        sync._build_garmin_workout(item)


def test_build_garmin_workout_rest_s_without_sets_raises():
    item = _queue_item(steps=[{"type": "interval", "reps": 8, "rest_s": 20}])
    with pytest.raises(ValueError, match="invalid sets/rest_s"):
        sync._build_garmin_workout(item)


def test_build_garmin_workout_multiple_sets_groups_sequential_order():
    # Regression: stepOrder/childStepId must stay a running counter across the whole
    # tree — confirmed against a captured real hand-built workout, not reset per
    # group, and not skipping or reusing values for flat steps that follow.
    item = _queue_item(
        steps=[
            {
                "type": "interval",
                "reps": 8,
                "sets": 3,
                "rest_s": 20,
                "category": "BENCH_PRESS",
                "exercise_name": "BARBELL_BENCH_PRESS",
            },
            {
                "type": "interval",
                "reps": 8,
                "sets": 3,
                "rest_s": 60,
                "category": "SQUAT",
                "exercise_name": "BARBELL_BACK_SQUAT",
            },
            {"type": "cooldown", "duration_s": 300},
        ]
    )
    steps = sync._build_garmin_workout(item)["workoutSegments"][0]["workoutSteps"]
    assert len(steps) == 3

    group1, group2, cooldown = steps
    assert group1["stepOrder"] == 1
    assert group1["childStepId"] == 1
    assert [s["stepOrder"] for s in group1["workoutSteps"]] == [2, 3]

    assert group2["stepOrder"] == 4
    assert group2["childStepId"] == 2
    assert [s["stepOrder"] for s in group2["workoutSteps"]] == [5, 6]

    assert cooldown["stepOrder"] == 7
    assert cooldown["childStepId"] is None


def test_build_garmin_workout_rest_distinct_from_recovery():
    item = _queue_item(
        steps=[
            {"type": "recovery", "duration_s": 60},
            {"type": "interval", "reps": 8, "sets": 2, "rest_s": 30},
        ]
    )
    steps = sync._build_garmin_workout(item)["workoutSegments"][0]["workoutSteps"]
    recovery_step = steps[0]
    rest_step = steps[1]["workoutSteps"][1]
    assert recovery_step["stepType"] == {"stepTypeId": 4, "stepTypeKey": "recovery"}
    assert rest_step["stepType"] == {"stepTypeId": 5, "stepTypeKey": "rest"}
    assert recovery_step["stepType"] != rest_step["stepType"]
