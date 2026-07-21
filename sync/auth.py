"""
One-time interactive auth helper for Garmin accounts with MFA.

Usage (from repo root):
  podman run --rm -it --env-file .env -v waypoint_sync_data:/data \
    localhost/waypoint_sync python auth.py

Then copy tokens into the sync container if using a separate volume:
  podman cp waypoint_sync_data:/data/garmin_auth ./auth_tokens/garmin_auth

IMPORTANT: copy only the garmin_auth dir, never the whole /data dir — /data
also holds sync_state.json, and dragging that along seeds the target with
stale watermarks, silently skipping the initial BACKFILL_DAYS backfill.
"""

import logging
import os
from pathlib import Path

from garminconnect import Garmin

logging.basicConfig(level=logging.DEBUG, format="%(name)s %(levelname)s %(message)s")

email = os.environ["GARMIN_EMAIL"]
password = os.environ["GARMIN_PASSWORD"]
data_dir = Path(os.environ.get("DATA_DIR", "/data"))
token_store = str(data_dir / "garmin_auth")

data_dir.mkdir(parents=True, exist_ok=True)

print(f"Authenticating {email} ...")
garmin = Garmin(email=email, password=password, prompt_mfa=lambda: input("MFA code: "))
# Skip mobile strategies — account is rate-limited on the mobile API.
# Portal+cffi uses clientId=GarminConnect which matches the real website
# and triggers MFA email delivery.
garmin.client.skip_strategies = {"mobile+cffi", "mobile+requests", "widget+cffi"}
garmin.login()
garmin.client.dump(token_store)
print(f"Token saved to {token_store}")
