#!/usr/bin/env python3
"""Regenerate internal/garmin/exercises/catalog.json from a raw Garmin exercise dump.

Garmin Connect's workout editor has no documented API for the strength-exercise
catalog (confirmed: /workout-service/exercises, /workout-service/exercise/categories,
/workout-service/workout/exercises, /workout-service/exerciseTypes all 404). The
catalog is a static asset the editor's exercise picker fetches directly:

    GET https://connect.garmin.com/web-api/web-data/exercises/Exercises.json

Direct requests to that URL 403 without the browser's session context (cookies +
same-origin fetch), so it must be captured from an authenticated browser session,
not curled directly.

How to capture the raw dump (verified 2026-07-28, against a real logged-in account):
1. Open https://connect.garmin.com/app/workouts, click "Create a Workout", pick any
   strength-adjacent type (Strength Training/HIIT/Cardio all load the same picker).
2. In the browser console (same origin), run:
       const r = await fetch('/web-api/web-data/exercises/Exercises.json', {credentials: 'include'});
       const text = await r.text();
   and get `text` out of the page — e.g. dump it via console.log and read from
   DevTools/automation, since the raw file is ~200KB and won't fit in a single
   return value from most tool-call channels.
3. Save the extracted JSON text as the raw dump file, then run this script:
       python3 scripts/generate_exercise_catalog.py path/to/raw_dump.json

Do not trust a third-party project's vendored catalog as the source of truth — verify
against your own account's live picker (Garmin exercise availability can differ by
account/region, and third-party scrapes go stale). A reference catalog exists at
cyberjunky/python-garminconnect (garminconnect/exercises.py) and is useful only as a
naming-convention sanity check, not as ground truth.

Raw dump shape (from Exercises.json):
    {"categories": {"<CATEGORY_KEY>": {"exercises": {"<EXERCISE_KEY>": {
        "primaryMuscles": [...], "secondaryMuscles": [...]}, ...}}, ...}}

Output shape (internal/garmin/exercises/catalog.json) — flat list, sorted by
(category, exercise_name), consumed by internal/garmin/exercises/catalog.go via
go:embed:
    [{"category": "...", "exercise_name": "...", "display_name": "...",
      "primary_muscles": [...], "secondary_muscles": [...]}, ...]

display_name is derived (title-cased, underscores to spaces) — Garmin's raw dump
has no human-readable label, only the enum keys.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

OUT = (
    Path(__file__).resolve().parent.parent
    / "internal"
    / "garmin"
    / "exercises"
    / "catalog.json"
)


def _display_name(key: str) -> str:
    return " ".join(word.capitalize() for word in key.split("_"))


def build_catalog(raw: dict) -> list[dict]:
    rows = []
    for category, cat_data in raw["categories"].items():
        for exercise_name, meta in cat_data["exercises"].items():
            rows.append(
                {
                    "category": category,
                    "exercise_name": exercise_name,
                    "display_name": _display_name(exercise_name),
                    "primary_muscles": meta.get("primaryMuscles", []),
                    "secondary_muscles": meta.get("secondaryMuscles", []),
                }
            )
    rows.sort(key=lambda r: (r["category"], r["exercise_name"]))
    return rows


def main() -> None:
    if len(sys.argv) != 2:
        print(__doc__, file=sys.stderr)
        sys.exit(1)

    raw = json.loads(Path(sys.argv[1]).read_text())
    catalog = build_catalog(raw)

    categories = {row["category"] for row in catalog}
    print(f"{len(catalog)} exercises across {len(categories)} categories")

    OUT.write_text(json.dumps(catalog, indent=2) + "\n")
    print(f"wrote {OUT}")


if __name__ == "__main__":
    main()
