"""Validate live garminconnect API responses against sync/schemas/*.schema.json.

Turns CLAUDE.md's "verify before writing" rule from a manual eyeball check into
something inspect_api.py can enforce automatically (#55). Not a general-purpose
validator — only methods listed in METHOD_SCHEMA have a schema to check against.
"""

from __future__ import annotations

import functools
import json
import re
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


@functools.cache
def _load_schema(schema_file: str) -> dict[str, Any]:
    """Read+parse one schema file. Cached: schema files don't change within a
    process's lifetime, and validate() may now be called many times per run
    (e.g. once per day in a 90-day backfill) — see drift_check.py."""
    return json.loads((SCHEMA_DIR / schema_file).read_text())  # type: ignore[no-any-return]


def validate(method_name: str, instance: Any) -> list[str] | None:
    """Validate instance against method_name's schema.

    Returns None if no schema exists for this method (nothing to check against
    yet — not a failure). Otherwise returns a list of human-readable error
    messages, sorted for stable output; empty list means valid.
    """
    schema_file = METHOD_SCHEMA.get(method_name)
    if schema_file is None:
        return None

    schema = _load_schema(schema_file)
    validator = Draft202012Validator(schema, registry=_load_registry())

    errors = []
    for err in validator.iter_errors(instance):
        path = "/".join(str(p) for p in err.absolute_path) or "<root>"
        errors.append(f"{path}: {err.message}")
    return sorted(errors)


def _type_includes(schema: dict[str, Any], type_name: str) -> bool:
    """ "type" is either a bare string or a list (e.g. ["object", "null"] on a
    field marked nullable) — check membership either way."""
    t = schema.get("type")
    return t == type_name or (isinstance(t, list) and type_name in t)


def _walk_new_fields(
    schema: dict[str, Any], instance: Any, path: str, resolver: Any, found: list[str]
) -> None:
    if "$ref" in schema:
        resolved = resolver.lookup(schema["$ref"])
        _walk_new_fields(resolved.contents, instance, path, resolved.resolver, found)
        return

    if isinstance(instance, dict) and _type_includes(schema, "object"):
        props: dict[str, Any] | None = schema.get("properties")
        pattern_props: dict[str, Any] | None = schema.get("patternProperties")
        for key, value in instance.items():
            sub_path = f"{path}/{key}" if path else key
            if props is not None and key in props:
                _walk_new_fields(props[key], value, sub_path, resolver, found)
                continue
            if pattern_props is not None:
                matched_schema = next(
                    (s for pat, s in pattern_props.items() if re.search(pat, key)), None
                )
                if matched_schema is not None:
                    _walk_new_fields(matched_schema, value, sub_path, resolver, found)
                    continue
            if props is not None or pattern_props is not None:
                found.append(sub_path)
            # else: this object node declares neither properties nor
            # patternProperties (fully untyped) — nothing to compare against.
    elif isinstance(instance, list) and _type_includes(schema, "array"):
        items_schema = schema.get("items")
        if items_schema is not None:
            for i, item in enumerate(instance):
                sub_path = f"{path}/{i}" if path else str(i)
                _walk_new_fields(items_schema, item, sub_path, resolver, found)


def find_new_fields(method_name: str, instance: Any) -> list[str] | None:
    """Find fields present in a live response but not declared in method_name's schema.

    additionalProperties: true (used throughout sync/schemas/) means an undeclared
    field never fails validate() — Garmin adding a field isn't treated as a breaking
    change. This is the other half: surfacing new fields so a human can decide
    whether to start syncing them (#68 follow-up). Recurses into every nested object
    the schema declares "properties" for, resolving $ref (e.g. the shared
    vo2max.schema.json defs) along the way.

    Returns None if no schema exists for this method (same convention as validate()).
    Otherwise a sorted list of slash-separated paths to fields not declared in any
    "properties" the schema enumerates. Object nodes keyed only by patternProperties
    (e.g. metricsTrainingLoadBalanceDTOMap, keyed by device ID) are matched against
    the pattern instead — a new device ID isn't "a new field," by design there's no
    fixed field list to compare against there.
    """
    schema_file = METHOD_SCHEMA.get(method_name)
    if schema_file is None:
        return None

    schema = _load_schema(schema_file)
    resolver = _load_registry().resolver(base_uri=schema["$id"])
    found: list[str] = []
    _walk_new_fields(schema, instance, "", resolver, found)
    return sorted(found)
