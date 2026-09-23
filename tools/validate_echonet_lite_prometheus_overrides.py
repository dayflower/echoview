#!/usr/bin/env python3
"""Validate Appendix-derived Prometheus export policy overrides."""

from __future__ import annotations

import argparse
import re
from pathlib import Path
from typing import Any, Mapping

import yaml

from _override_validation import (
    ValidationError,
    fail as _fail,
    keys as _keys,
    mapping as _mapping,
    validate_appendix_reference,
    validate_appendix_source,
    validate_class_code,
    validate_epc,
)

PROJECT_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_INPUT = PROJECT_ROOT / "echonet_lite_prometheus_overrides.yaml"
EDT_PATTERN = re.compile(r"0x(?:[0-9A-F]{2})+")
MAX_EXACT_PROMETHEUS_INTEGER = 1 << 53


def _policy(value: Any, path: str) -> None:
    policy = _mapping(value, path)
    export = policy.get("export")
    if export == "enum_map":
        _keys(
            policy,
            path,
            required={"export", "enum_map"},
            allowed={"export", "enum_map"},
        )
        enum_map = _mapping(policy["enum_map"], f"{path}.enum_map")
        _keys(enum_map, f"{path}.enum_map", required={"values"}, allowed={"values"})
        values = _mapping(enum_map["values"], f"{path}.enum_map.values")
        if not values:
            _fail(f"{path}.enum_map.values", "must not be empty")
        for edt, code in values.items():
            if not EDT_PATTERN.fullmatch(edt):
                _fail(
                    f"{path}.enum_map.values key {edt!r}",
                    "must be an uppercase complete EDT",
                )
            if type(code) is not int or code == -1:
                _fail(
                    f"{path}.enum_map.values.{edt}",
                    "must be an integer other than reserved -1",
                )
            if (
                not -MAX_EXACT_PROMETHEUS_INTEGER
                <= code
                <= MAX_EXACT_PROMETHEUS_INTEGER
            ):
                _fail(
                    f"{path}.enum_map.values.{edt}",
                    "must be exactly representable by Prometheus",
                )
        return
    if export == "raw_uint":
        _keys(
            policy,
            path,
            required={"export", "raw_uint"},
            allowed={"export", "raw_uint"},
        )
        raw_uint = _mapping(policy["raw_uint"], f"{path}.raw_uint")
        _keys(raw_uint, f"{path}.raw_uint", required={"bytes"}, allowed={"bytes"})
        if type(raw_uint["bytes"]) is not int or not 1 <= raw_uint["bytes"] <= 6:
            _fail(f"{path}.raw_uint.bytes", "must be an integer from 1 through 6")
        return
    _fail(f"{path}.export", "must be 'enum_map' or 'raw_uint'")


def validate_overrides(document: Any) -> None:
    """Raise ValidationError unless document matches the overrides schema."""
    root = _mapping(document, "document")
    _keys(
        root,
        "document",
        required={"prometheus_overrides_format", "source", "classes"},
        allowed={"prometheus_overrides_format", "source", "classes"},
    )
    if root["prometheus_overrides_format"] != 1:
        _fail("document.prometheus_overrides_format", "must be the integer 1")
    source = _mapping(root["source"], "document.source")
    _keys(source, "document.source", required={"appendix"}, allowed={"appendix"})
    validate_appendix_reference(source["appendix"], "document.source.appendix")
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
                required={"prometheus", "x-source"},
                allowed={"prometheus", "x-source"},
            )
            _policy(property_definition["prometheus"], f"{property_path}.prometheus")
            validate_appendix_source(
                property_definition["x-source"], f"{property_path}.x-source"
            )


def load_and_validate(path: Path = DEFAULT_INPUT) -> Mapping[str, Any]:
    """Read one overrides document and validate its complete structure."""
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
