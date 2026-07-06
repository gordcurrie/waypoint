"""
One-time interactive auth helper for Garmin accounts with MFA.

Usage (run from repo root):
  GARMIN_EMAIL=you@example.com GARMIN_PASSWORD=secret \
    DATA_DIR=./auth_tokens python sync/auth.py

Then mount the token into the sync container or copy it into the volume:
  podman cp ./auth_tokens/garmin_auth waypoint_sync_1:/data/garmin_auth

Or, point DATA_DIR at the volume's bind path for your setup.
"""

import os
from pathlib import Path

from garminconnect import Garmin

email    = os.environ["GARMIN_EMAIL"]
password = os.environ["GARMIN_PASSWORD"]
data_dir = Path(os.environ.get("DATA_DIR", "/data"))
token_store = str(data_dir / "garmin_auth")

data_dir.mkdir(parents=True, exist_ok=True)

print(f"Authenticating {email} ...")
garmin = Garmin(email=email, password=password, prompt_mfa=lambda: input("MFA code: "))
garmin.login()
garmin.garth.dump(token_store)
print(f"Token saved to {token_store}")
