"""Validate live garminconnect API responses against sync/schemas/*.schema.json.

Turns CLAUDE.md's "verify before writing" rule from a manual eyeball check into
something inspect_api.py can enforce automatically (#55). Not a general-purpose
validator — only methods listed in METHOD_SCHEMA have a schema to check against.
"""

from __future__ import annotations

import functools
import json
from pathlib import Path
from typing import Any

from jsonschema import Draft202012Validator
from referencing import Registry, Resource

SCHEMA_DIR = Path(__file__).parent / "schemas"

# garminconnect method name -> schema file. Kept in sync with the table in
# sync/schemas/README.md; a method missing here just means "no schema derived
# yet", not a hard failure — see validate()'s None-schema return.
METHOD_SCHEMA = {
    "get_activities_by_date": "activities.schema.json",
    "get_stats": "daily_stats.schema.json",
    "get_sleep_data": "sleep.schema.json",
    "get_hrv_data": "hrv.schema.json",
    "get_training_readiness": "training_readiness.schema.json",
    "get_training_status": "training_status.schema.json",
    "get_max_metrics": "performance.schema.json",
    "get_lactate_threshold": "lactate_threshold.schema.json",
    "get_activity_splits": "activity_splits.schema.json",
    "get_activity_hr_in_timezones": "activity_hr_zones.schema.json",
    "get_scheduled_workouts": "scheduled_workouts.schema.json",
    "get_respiration_data": "respiration.schema.json",
    "get_adaptive_training_plan_by_id": "adaptive_training_plan.schema.json",
    "get_training_plans": "training_plans.schema.json",
}


@functools.lru_cache(maxsize=1)
def _load_registry() -> Registry:
    """Build a referencing.Registry from every schema file, keyed by its own $id.

    Needed so cross-file $ref (e.g. performance.schema.json -> vo2max.schema.json)
    resolves — each schema's $id doubles as the base URI relative $refs resolve
    against. Cached: schema files don't change within a process's lifetime, and
    every validate() call would otherwise re-read and re-parse all of them.
    """
    resources = {}
    for path in SCHEMA_DIR.glob("*.schema.json"):
        contents = json.loads(path.read_text())
        if "$id" not in contents:
            raise ValueError(f"{path} is missing required $id")
        resources[contents["$id"]] = Resource.from_contents(contents)
    return Registry().with_resources(resources.items())


def validate(method_name: str, instance: Any) -> list[str] | None:
    """Validate instance against method_name's schema.

    Returns None if no schema exists for this method (nothing to check against
    yet — not a failure). Otherwise returns a list of human-readable error
    messages, sorted for stable output; empty list means valid.
    """
    schema_file = METHOD_SCHEMA.get(method_name)
    if schema_file is None:
        return None

    schema = json.loads((SCHEMA_DIR / schema_file).read_text())
    validator = Draft202012Validator(schema, registry=_load_registry())

    errors = []
    for err in validator.iter_errors(instance):
        path = "/".join(str(p) for p in err.absolute_path) or "<root>"
        errors.append(f"{path}: {err.message}")
    return sorted(errors)
