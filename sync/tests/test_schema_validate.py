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


def test_find_new_fields_unknown_method_returns_none():
    assert schema_validate.find_new_fields("get_something_not_covered", {}) is None


def test_find_new_fields_no_new_fields():
    instance = {
        "speed_and_heart_rate": {
            "calendarDate": "2026-07-06T11:42:53.885",
            "speed": 0.33888794,
            "heartRate": 165,
        },
        "power": {"functionalThresholdPower": 369},
    }
    assert schema_validate.find_new_fields("get_lactate_threshold", instance) == []


def test_find_new_fields_detects_top_level_and_nested():
    instance = {
        "speed_and_heart_rate": {
            "calendarDate": "2026-07-06T11:42:53.885",
            "speed": 0.33888794,
            "heartRate": 165,
            "brandNewNestedField": "x",
        },
        "power": {"functionalThresholdPower": 369},
        "brandNewTopLevelField": "y",
    }
    found = schema_validate.find_new_fields("get_lactate_threshold", instance)
    assert found == [
        "brandNewTopLevelField",
        "speed_and_heart_rate/brandNewNestedField",
    ]


def test_find_new_fields_resolves_ref():
    """performance.schema.json's "generic" field $refs into vo2max.schema.json —
    a new field inside it must still be caught, not silently skipped because the
    walker didn't resolve the $ref."""
    instance = [{"generic": {"calendarDate": "2026-01-01", "vo2MaxValue": 50, "brandNew": "z"}}]
    assert schema_validate.find_new_fields("get_max_metrics", instance) == ["0/generic/brandNew"]


def test_find_new_fields_ignores_new_pattern_property_keys():
    """A device-keyed dict (patternProperties) getting a new device ID isn't "a
    new field" — there's no fixed field list to compare a dict key against."""
    ts_instance = {
        "mostRecentTrainingLoadBalance": {
            "metricsTrainingLoadBalanceDTOMap": {
                "3620139022": {"calendarDate": "2026-08-05", "deviceId": 3620139022},
                "9999999999": {"calendarDate": "2026-08-05", "deviceId": 9999999999},
            }
        }
    }
    assert schema_validate.find_new_fields("get_training_status", ts_instance) == []

    ts_instance["mostRecentTrainingLoadBalance"]["metricsTrainingLoadBalanceDTOMap"]["3620139022"][
        "brandNewFieldInsideEntry"
    ] = "x"
    assert schema_validate.find_new_fields("get_training_status", ts_instance) == [
        "mostRecentTrainingLoadBalance/metricsTrainingLoadBalanceDTOMap/3620139022/brandNewFieldInsideEntry"
    ]
