"""Tests for drift_check.py — see PLAN.md's "Automatic Garmin API drift detection
(#68)" section for the design this implements."""

import json

import pytest

import drift_check

METHOD = "get_respiration_data"  # covered by schemas/respiration.schema.json
VALID_RESPONSE = {"lowestRespirationValue": 4.0, "highestRespirationValue": 18.0}
INVALID_RESPONSE = {"lowestRespirationValue": "not-a-number"}


class _FakeGarmin:
    def __init__(self, responses):
        self._responses = responses
        self.calls = 0

    def get_respiration_data(self, day):
        self.calls += 1
        return self._responses

    def get_full_name(self):
        return "unwrapped passthrough"


@pytest.fixture(autouse=True)
def _drift_state_in_tmp(tmp_path, monkeypatch):
    monkeypatch.setattr(drift_check, "DATA_DIR", tmp_path)
    monkeypatch.setattr(drift_check, "DRIFT_STATE_FILE", tmp_path / "drift_alert_state.json")


def test_wrap_calls_through_and_returns_result_unchanged():
    fake = _FakeGarmin(VALID_RESPONSE)
    wrapped = drift_check.wrap(fake)

    result = wrapped.get_respiration_data("2026-08-04")

    assert result == VALID_RESPONSE
    assert fake.calls == 1


@pytest.mark.parametrize("falsy_response", [None, {}, [], ""])
def test_falsy_response_skips_validation(monkeypatch, falsy_response):
    """None/{}/[] is what sync.py's own `or {}`/`or []`/`if raw:` guards treat as
    'no data for this day' at every call site — must not false-alarm on it."""
    validated = []
    monkeypatch.setattr(
        drift_check.schema_validate,
        "validate",
        lambda method, instance: validated.append((method, instance)) or [],
    )
    fake = _FakeGarmin(falsy_response)
    wrapped = drift_check.wrap(fake)

    result = wrapped.get_respiration_data("2026-08-04")

    assert result == falsy_response
    assert validated == []


def test_wrap_passes_through_methods_without_a_schema():
    fake = _FakeGarmin(VALID_RESPONSE)
    wrapped = drift_check.wrap(fake)

    assert wrapped.get_full_name() == "unwrapped passthrough"


def _recording_send_alert(alerts, succeed=True):
    def _send(method, kind, errors):
        alerts.append((method, kind))
        return succeed

    return _send


def test_schema_mismatch_logs_and_alerts(monkeypatch, caplog):
    alerts: list[tuple[str, str]] = []
    monkeypatch.setattr(drift_check, "_send_alert", _recording_send_alert(alerts))

    fake = _FakeGarmin(INVALID_RESPONSE)
    wrapped = drift_check.wrap(fake)

    with caplog.at_level("ERROR"):
        result = wrapped.get_respiration_data("2026-08-04")

    assert result == INVALID_RESPONSE  # still returned despite mismatch
    assert any("no longer matches its schema" in r.message for r in caplog.records)
    assert alerts == [(METHOD, "mismatch")]


def test_new_fields_logs_warning_and_alerts(monkeypatch, caplog):
    alerts: list[tuple[str, str]] = []
    monkeypatch.setattr(drift_check, "_send_alert", _recording_send_alert(alerts))
    response_with_new_field = {**VALID_RESPONSE, "brandNewField": "x"}

    fake = _FakeGarmin(response_with_new_field)
    wrapped = drift_check.wrap(fake)

    with caplog.at_level("WARNING"):
        result = wrapped.get_respiration_data("2026-08-04")

    assert result == response_with_new_field  # still returned unchanged
    assert any("new field(s) not in its schema" in r.message for r in caplog.records)
    assert alerts == [(METHOD, "new_fields")]


def test_mismatch_and_new_fields_alert_independently_same_day(monkeypatch):
    """A response that's both broken AND has a new field must alert for both —
    one kind's dedup must not suppress the other on the same method/day."""
    alerts: list[tuple[str, str]] = []
    monkeypatch.setattr(drift_check, "_send_alert", _recording_send_alert(alerts))
    response = {**INVALID_RESPONSE, "brandNewField": "x"}

    fake = _FakeGarmin(response)
    wrapped = drift_check.wrap(fake)

    wrapped.get_respiration_data("2026-08-04")

    assert sorted(alerts) == [(METHOD, "mismatch"), (METHOD, "new_fields")]


def test_alert_deduped_within_same_day(monkeypatch):
    alerts: list[tuple[str, str]] = []
    monkeypatch.setattr(drift_check, "_send_alert", _recording_send_alert(alerts))

    fake = _FakeGarmin(INVALID_RESPONSE)
    wrapped = drift_check.wrap(fake)

    wrapped.get_respiration_data("2026-08-04")
    wrapped.get_respiration_data("2026-08-04")

    assert alerts == [(METHOD, "mismatch")]  # second call same day, no second alert


def test_failed_send_is_not_deduped_and_retries_next_call(monkeypatch):
    """A failed send must not mark today as alerted — otherwise a transient
    webhook outage silently swallows the alert until tomorrow."""
    alerts: list[tuple[str, str]] = []
    monkeypatch.setattr(drift_check, "_send_alert", _recording_send_alert(alerts, succeed=False))

    fake = _FakeGarmin(INVALID_RESPONSE)
    wrapped = drift_check.wrap(fake)

    wrapped.get_respiration_data("2026-08-04")
    wrapped.get_respiration_data("2026-08-04")

    # retried both times, never deduped
    assert alerts == [(METHOD, "mismatch"), (METHOD, "mismatch")]


def test_validation_exception_is_contained(monkeypatch, caplog):
    """A bug in drift-checking itself (corrupt schema, disk full, whatever) must
    never cost the caller its real Garmin data for this cycle."""
    monkeypatch.setattr(
        drift_check.schema_validate,
        "validate",
        lambda method, instance: (_ for _ in ()).throw(RuntimeError("boom")),
    )

    fake = _FakeGarmin(VALID_RESPONSE)
    wrapped = drift_check.wrap(fake)

    with caplog.at_level("ERROR"):
        result = wrapped.get_respiration_data("2026-08-04")

    assert result == VALID_RESPONSE
    assert any("failed unexpectedly" in r.message for r in caplog.records)


def test_alert_state_persisted_across_wrap_instances(tmp_path):
    drift_check._maybe_alert(METHOD, "mismatch", ["some error"])

    state = json.loads((tmp_path / "drift_alert_state.json").read_text())
    assert state[METHOD]["mismatch"]


def test_send_alert_noop_without_webhook_url(monkeypatch):
    monkeypatch.setattr(drift_check, "ALERT_WEBHOOK_URL", "")
    calls = []
    monkeypatch.setattr(drift_check.urllib.request, "urlopen", lambda *a, **k: calls.append(1))

    assert drift_check._send_alert(METHOD, "mismatch", ["some error"]) is True
    assert calls == []


def test_send_alert_failure_does_not_raise_and_returns_false(monkeypatch):
    monkeypatch.setattr(drift_check, "ALERT_WEBHOOK_URL", "http://example.invalid/webhook")

    def _boom(*a, **k):
        raise OSError("connection refused")

    monkeypatch.setattr(drift_check.urllib.request, "urlopen", _boom)

    # must not raise
    assert drift_check._send_alert(METHOD, "mismatch", ["some error"]) is False
