#!/usr/bin/env python3
"""Validate reviewed MRA-derived Prometheus metric-type overrides."""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Any, Mapping

import yaml

from _override_validation import (
    ValidationError,
    fail as _fail,
    keys as _keys,
    mapping as _mapping,
    non_empty_string as _string,
    validate_class_code,
    validate_epc,
)

PROJECT_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_INPUT = PROJECT_ROOT / "echonet_lite_prometheus_metric_type_overrides.yaml"


def _source(value: Any, path: str) -> None:
    source = _mapping(value, path)
    _keys(source, path, required={"mra"}, allowed={"mra"})
    mra = _mapping(source["mra"], f"{path}.mra")
    _keys(
        mra,
        f"{path}.mra",
        required={"release", "data_version", "classifier"},
        allowed={"release", "data_version", "classifier"},
    )
    for field in ("release", "data_version", "classifier"):
        _string(mra[field], f"{path}.mra.{field}")


def validate_overrides(document: Any) -> None:
    """Raise ValidationError unless document matches the metric-type schema."""
    root = _mapping(document, "document")
    _keys(
        root,
        "document",
        required={"prometheus_metric_type_overrides_format", "source", "classes"},
        allowed={"prometheus_metric_type_overrides_format", "source", "classes"},
    )
    if root["prometheus_metric_type_overrides_format"] != 1:
        _fail(
            "document.prometheus_metric_type_overrides_format", "must be the integer 1"
        )
    _source(root["source"], "document.source")
    classes = _mapping(root["classes"], "document.classes")
    for class_code, raw_class in classes.items():
        class_path = f"document.classes.{class_code}"
        validate_class_code(class_code, class_path)
        class_definition = _mapping(raw_class, class_path)
        _keys(
            class_definition,
            class_path,
            required={"properties"},
            allowed={"properties"},
        )
        properties = _mapping(
            class_definition["properties"], f"{class_path}.properties"
        )
        if not properties:
            _fail(f"{class_path}.properties", "must not be empty")
        for epc, raw_property in properties.items():
            property_path = f"{class_path}.properties.{epc}"
            validate_epc(epc, property_path)
            property_definition = _mapping(raw_property, property_path)
            _keys(
                property_definition,
                property_path,
                required={"prometheus"},
                allowed={"prometheus"},
            )
            policy = _mapping(
                property_definition["prometheus"], f"{property_path}.prometheus"
            )
            _keys(
                policy,
                f"{property_path}.prometheus",
                required={"metric_type"},
                allowed={"metric_type"},
            )
            if policy["metric_type"] != "counter":
                _fail(f"{property_path}.prometheus.metric_type", "must be 'counter'")


def load_and_validate(path: Path = DEFAULT_INPUT) -> Mapping[str, Any]:
    """Read one metric-type overrides document and validate its schema."""
    try:
        document = yaml.safe_load(path.read_text())
    except (OSError, yaml.YAMLError) as error:
        raise ValidationError(f"cannot read {path}: {error}") from error
    validate_overrides(document)
    return _mapping(document, "document")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("path", nargs="?", type=Path, default=DEFAULT_INPUT)
    args = parser.parse_args()
    try:
        load_and_validate(args.path)
    except ValidationError as error:
        print(f"error: {error}")
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
