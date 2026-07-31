"""Tests for schema_validate.py — live-response validation against sync/schemas/*.schema.json."""

import schema_validate


def test_unknown_method_returns_none():
    assert schema_validate.validate("get_something_not_covered", {}) is None


def test_valid_lactate_threshold_response_passes():
    instance = {
        "speed_and_heart_rate": {
            "calendarDate": "2026-07-06T11:42:53.885",
            "speed": 0.33888794,
            "heartRate": 165,
        },
        "power": {
            "functionalThresholdPower": 369,
        },
    }
    errors = schema_validate.validate("get_lactate_threshold", instance)
    assert errors == []


def test_invalid_lactate_threshold_response_fails():
    instance = {"speed_and_heart_rate": {"speed": "not a number", "heartRate": 165}}
    errors = schema_validate.validate("get_lactate_threshold", instance)
    assert errors
    assert any("speed" in e for e in errors)


def test_cross_file_ref_resolves():
    """performance.schema.json $refs into vo2max.schema.json — must resolve, not error."""
    instance = [{"generic": {"calendarDate": "2026-01-01", "vo2MaxValue": 50}}]
    errors = schema_validate.validate("get_max_metrics", instance)
    assert errors == []


def test_cross_file_ref_catches_real_violation():
    instance = [{"generic": {"calendarDate": "2026-01-01", "vo2MaxValue": "fifty"}}]
    errors = schema_validate.validate("get_max_metrics", instance)
    assert errors
