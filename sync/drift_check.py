"""Automatic Garmin API drift detection (#68).

sync.py already calls every schema-covered garminconnect method every sync cycle —
this wraps the logged-in Garmin client so those calls are validated against
sync/schemas/*.schema.json as a side effect of normal syncing, instead of relying on
someone remembering to run inspect_api.py by hand. A validation failure is never
raised into the caller: it's logged and (optionally) alerted, but the real response
is still returned unmodified so sync keeps working even if a schema itself is stale.

Two independent things get checked per call, alerted/deduped separately (see the
"kind" argument threaded through _maybe_alert/_send_alert below):
- "mismatch": a field's type/nesting no longer matches the schema — something's
  actually broken (or the schema needs loosening, see sync/schemas/README.md).
- "new_fields": additionalProperties:true means a field Garmin added doesn't fail
  validate() — this surfaces it anyway, so a human can decide whether to start
  syncing it (schema_validate.find_new_fields, #68 follow-up).

Off by default (DRIFT_CHECK_ENABLED) — this hits a personal Garmin account and a
personal alert webhook, not something every clone of this repo should do silently.
See sync.py's _login_and_wrap() for the gate; nothing in this module reads that flag
itself, wrap() just doesn't get called when it's off.
"""

from __future__ import annotations

import json
import logging
import os
import urllib.error
import urllib.request
from datetime import date
from pathlib import Path
from typing import Any

import schema_validate

log = logging.getLogger(__name__)

DATA_DIR = Path(os.environ.get("DATA_DIR", "/data"))
DRIFT_STATE_FILE = DATA_DIR / "drift_alert_state.json"
ALERT_WEBHOOK_URL = os.environ.get("DRIFT_ALERT_WEBHOOK_URL", "")


def _load_drift_state() -> dict[str, dict[str, str]]:
    if DRIFT_STATE_FILE.exists():
        try:
            raw: dict[str, Any] = json.loads(DRIFT_STATE_FILE.read_text())
        except (json.JSONDecodeError, OSError):
            log.warning("Drift alert state file corrupt — resetting to empty state")
            return {}
        # Migrate the pre-#82 flat {method: date} shape (mismatch-only, no "kind")
        # to {method: {kind: date}} so every caller can assume the new shape —
        # deployed installs already have real state files in the old format.
        return {
            method: ({"mismatch": value} if isinstance(value, str) else value)
            for method, value in raw.items()
        }
    return {}


def _save_drift_state(state: dict[str, dict[str, str]]) -> None:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    tmp = DRIFT_STATE_FILE.with_suffix(".tmp")
    tmp.write_text(json.dumps(state, indent=2))
    tmp.replace(DRIFT_STATE_FILE)


def _send_alert(method_name: str, kind: str, errors: list[str]) -> bool:
    """POST the alert. Returns True if it's safe to mark today as alerted —
    i.e. nothing was configured to send, or the send actually succeeded.
    False means the send failed and should be retried on the next call."""
    if not ALERT_WEBHOOK_URL:
        return True
    payload = json.dumps(
        {"method": method_name, "kind": kind, "date": date.today().isoformat(), "errors": errors}
    ).encode()
    req = urllib.request.Request(
        ALERT_WEBHOOK_URL,
        data=payload,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=5):
            pass
        return True
    except (urllib.error.URLError, OSError) as exc:
        log.error("drift_check: failed to send alert for %s (%s): %s", method_name, kind, exc)
        return False


def _maybe_alert(method_name: str, kind: str, errors: list[str]) -> None:
    """kind is "mismatch" or "new_fields" — deduped independently per method, so
    one doesn't suppress an alert for the other on the same method/day."""
    today = date.today().isoformat()
    state = _load_drift_state()
    method_state = state.setdefault(method_name, {})
    if method_state.get(kind) == today:
        return  # already alerted for this method+kind today
    if _send_alert(method_name, kind, errors):
        method_state[kind] = today
        _save_drift_state(state)
    # else: leave state alone so a transient send failure gets retried next cycle


class _DriftCheckingGarmin:
    """Proxy that validates schema-covered method calls, passes everything else through."""

    def __init__(self, garmin: Any) -> None:
        self._garmin = garmin

    def __getattr__(self, name: str) -> Any:
        attr = getattr(self._garmin, name)
        if name not in schema_validate.METHOD_SCHEMA or not callable(attr):
            return attr

        def _checked(*args: Any, **kwargs: Any) -> Any:
            result = attr(*args, **kwargs)
            if not result:
                # Every sync_* call site treats a falsy response (None/{}/[]) as
                # "no data for this day/item" and handles it with its own
                # `or {}`/`or []`/`if raw:` guard — never an error. None of the
                # schemas model that as valid, so skip validation rather than
                # false-alarm on routine empty days.
                return result
            try:
                errors = schema_validate.validate(name, result)
                if errors:
                    log.error(
                        "drift_check: %s response no longer matches its schema (%d error(s)): %s",
                        name,
                        len(errors),
                        "; ".join(errors),
                    )
                    _maybe_alert(name, "mismatch", errors)

                new_fields = schema_validate.find_new_fields(name, result)
                if new_fields:
                    log.warning(
                        "drift_check: %s response has new field(s) not in its schema (%d): %s",
                        name,
                        len(new_fields),
                        "; ".join(new_fields),
                    )
                    _maybe_alert(name, "new_fields", new_fields)
            except Exception as exc:
                # Drift-checking itself must never take down real syncing — a
                # corrupt schema file or a full disk here must not cost the
                # caller its actual Garmin data for this cycle.
                log.error("drift_check: checking %s failed unexpectedly: %s", name, exc)
            return result

        return _checked


def wrap(garmin: Any) -> Any:
    """Wrap a logged-in Garmin client so schema-covered calls are drift-checked."""
    return _DriftCheckingGarmin(garmin)
