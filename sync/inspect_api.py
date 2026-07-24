#!/usr/bin/env python3
"""
Dump a raw Garmin API response to stdout.

Usage (run inside the sync container):
  docker exec waypoint-sync-1 python3 /app/inspect_api.py <method> [arg ...]

Examples:
  docker exec waypoint-sync-1 python3 /app/inspect_api.py get_training_status 2026-07-24
  docker exec waypoint-sync-1 python3 /app/inspect_api.py get_training_readiness 2026-07-24
  docker exec waypoint-sync-1 python3 /app/inspect_api.py get_sleep_data 2026-07-24
  docker exec waypoint-sync-1 python3 /app/inspect_api.py get_hrv_data 2026-07-24
  docker exec waypoint-sync-1 python3 /app/inspect_api.py get_stats 2026-07-24

The output is the raw JSON from Garmin Connect — use it to verify field names
and response structure before writing any sync code.
"""

import json
import sys

sys.path.insert(0, "/app")
import sync  # noqa: E402

if len(sys.argv) < 2:
    print(__doc__, file=sys.stderr)
    sys.exit(1)

method_name = sys.argv[1]
args = sys.argv[2:]

g = sync._garmin_login()
method = getattr(g, method_name)
result = method(*args)
print(json.dumps(result, indent=2, default=str))
