"""Tests for drift_check.py — see PLAN.md's "Next: automatic Garmin API drift
detection (#68)" section for the design this implements."""

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


def test_schema_mismatch_logs_and_alerts(monkeypatch, caplog):
    alerts = []
    monkeypatch.setattr(drift_check, "_send_alert", lambda method, errors: alerts.append(method))

    fake = _FakeGarmin(INVALID_RESPONSE)
    wrapped = drift_check.wrap(fake)

    with caplog.at_level("ERROR"):
        result = wrapped.get_respiration_data("2026-08-04")

    assert result == INVALID_RESPONSE  # still returned despite mismatch
    assert any("no longer matches its schema" in r.message for r in caplog.records)
    assert alerts == [METHOD]


def test_alert_deduped_within_same_day(monkeypatch):
    alerts = []
    monkeypatch.setattr(drift_check, "_send_alert", lambda method, errors: alerts.append(method))

    fake = _FakeGarmin(INVALID_RESPONSE)
    wrapped = drift_check.wrap(fake)

    wrapped.get_respiration_data("2026-08-04")
    wrapped.get_respiration_data("2026-08-04")

    assert alerts == [METHOD]  # second call same day, no second alert


def test_alert_state_persisted_across_wrap_instances(tmp_path):
    drift_check._maybe_alert(METHOD, ["some error"])

    state = json.loads((tmp_path / "drift_alert_state.json").read_text())
    assert METHOD in state


def test_send_alert_noop_without_webhook_url(monkeypatch):
    monkeypatch.setattr(drift_check, "ALERT_WEBHOOK_URL", "")
    calls = []
    monkeypatch.setattr(drift_check.urllib.request, "urlopen", lambda *a, **k: calls.append(1))

    drift_check._send_alert(METHOD, ["some error"])

    assert calls == []


def test_send_alert_failure_does_not_raise(monkeypatch):
    monkeypatch.setattr(drift_check, "ALERT_WEBHOOK_URL", "http://example.invalid/webhook")

    def _boom(*a, **k):
        raise OSError("connection refused")

    monkeypatch.setattr(drift_check.urllib.request, "urlopen", _boom)

    drift_check._send_alert(METHOD, ["some error"])  # must not raise
