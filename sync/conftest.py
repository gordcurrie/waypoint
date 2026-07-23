"""Set required env vars before sync.py is imported (module-level reads os.environ)."""

import os

import pytest

os.environ.setdefault("GARMIN_EMAIL", "test@test.com")
os.environ.setdefault("GARMIN_PASSWORD", "testpass")
os.environ.setdefault("INFLUXDB_URL", "http://localhost:8181")

import sync  # noqa: E402  (must come after env setup)


@pytest.fixture(autouse=True)
def _safe_data_dir(tmp_path, monkeypatch):
    """Redirect all file I/O away from /data so tests run without Docker volumes."""
    monkeypatch.setattr(sync, "DATA_DIR", tmp_path)
    monkeypatch.setattr(sync, "TOKEN_STORE", str(tmp_path / "garmin_auth"))
    monkeypatch.setattr(sync, "STATE_FILE", tmp_path / "sync_state.json")


@pytest.fixture()
def no_sleep(monkeypatch):
    """Suppress time.sleep calls so tests that trigger API rate-limit delays run instantly."""
    monkeypatch.setattr(sync.time, "sleep", lambda _: None)


@pytest.fixture(autouse=True)
def no_delete(monkeypatch):
    """Suppress the InfluxDB DELETE HTTP call so tests don't hit a real server."""
    monkeypatch.setattr(sync, "_delete_scheduled_workouts", lambda *_: None)
