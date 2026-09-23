#!/usr/bin/env python3
"""Validate the structural schema of an ECHONET Lite catalog document."""

from __future__ import annotations

import argparse
import re
from pathlib import Path
from typing import Any, Mapping

import yaml

from _override_validation import (
    ValidationError,
    fail,
    mapping,
    non_empty_string,
    positive_int,
    validate_class_code,
    validate_epc,
)

DEFAULT_INPUT = (
    Path(__file__).resolve().parent.parent / "catalog" / "echonet_lite_catalog.yaml"
)
EDT_PATTERN = re.compile(r"0x(?:[0-9A-F]{2})+")
METRIC_NAME_PATTERN = re.compile(r"[a-zA-Z_][a-zA-Z0-9_]*")
ACCESS_VALUES = {"required", "optional", "notApplicable", "required_c", "required_o"}
CODEC_KINDS = {
    "enum",
    "uint",
    "int",
    "array",
    "raw",
    "time",
    "date",
    "date_time",
    "text",
    "encoded_text",
    "bitmap",
    "property_map",
    "instance_list",
}


class _UniqueKeyLoader(yaml.SafeLoader):
    """Reject YAML mappings that silently replace an earlier key."""

    def construct_mapping(self, node: yaml.MappingNode, deep: bool = False) -> dict:
        self.flatten_mapping(node)
        result = {}
        for key_node, value_node in node.value:
            key = self.construct_object(key_node, deep=deep)
            try:
                duplicate = key in result
            except TypeError as error:
                raise yaml.constructor.ConstructorError(
                    None, None, "unhashable mapping key", key_node.start_mark
                ) from error
            if duplicate:
                raise yaml.constructor.ConstructorError(
                    None, None, f"duplicate key {key!r}", key_node.start_mark
                )
            result[key] = self.construct_object(value_node, deep=deep)
        return result


def _string(value: Any, path: str) -> None:
    non_empty_string(value, path)


def _integer(value: Any, path: str, *, minimum: int | None = None) -> None:
    if type(value) is not int or (minimum is not None and value < minimum):
        fail(
            path,
            "must be an integer"
            if minimum is None
            else f"must be an integer >= {minimum}",
        )


def _fields(value: Mapping[str, Any], path: str, validators: Mapping[str, Any]) -> None:
    for field, validate in validators.items():
        if field in value:
            validate(value[field], f"{path}.{field}")


def _localized(value: Any, path: str) -> None:
    names = mapping(value, path)
    if not any(field in names for field in ("ja", "en")):
        fail(path, "must contain ja or en")
    _fields(names, path, {"ja": _string, "en": _string})


def _class_code_list(value: Any, path: str) -> None:
    if not isinstance(value, list):
        fail(path, "must be a list")
    for index, item in enumerate(value):
        validate_class_code(item, f"{path}[{index}]")


def _profile_id_list(value: Any, path: str) -> None:
    if not isinstance(value, list):
        fail(path, "must be a list")
    for index, item in enumerate(value):
        _string(item, f"{path}[{index}]")


def _edt(value: Any, path: str) -> None:
    if not isinstance(value, str) or not EDT_PATTERN.fullmatch(value):
        fail(path, "must be an uppercase complete 0x-prefixed EDT")


def _enum_values(value: Any, path: str) -> None:
    values = mapping(value, path)
    for edt, raw_entry in values.items():
        entry_path = f"{path}.{edt}"
        _edt(edt, entry_path)
        entry = mapping(raw_entry, entry_path)
        if "value" not in entry and "label" not in entry:
            fail(entry_path, "requires value or label")
        if "value" in entry and isinstance(entry["value"], (dict, list)):
            fail(f"{entry_path}.value", "must be a scalar")
        _fields(entry, entry_path, {"label": _localized})


def _codec_kind(value: Any, path: str) -> None:
    item = mapping(value, path)
    kind = item.get("kind")
    if not isinstance(kind, str) or kind not in CODEC_KINDS:
        fail(f"{path}.kind", f"must be a supported codec kind, got {kind!r}")
    common = {
        "unit": _string,
        "scale": _string,
        "offset": _string,
        "minimum": _string,
        "maximum": _string,
    }
    if kind == "enum":
        if "values" not in item:
            fail(path, "enum requires values")
        _fields(item, path, {"size": positive_int, "values": _enum_values})
    elif kind in {"uint", "int"}:
        if "bytes" not in item:
            fail(path, f"{kind} requires bytes")
        _integer(item["bytes"], f"{path}.bytes", minimum=1)
        if item["bytes"] > 8:
            fail(f"{path}.bytes", "must be at most 8")
        _fields(item, path, common)
    elif kind == "array":
        if "itemSize" not in item or "items" not in item:
            fail(path, "array requires itemSize and items")
        positive_int(item["itemSize"], f"{path}.itemSize")
        _fields(
            item,
            path,
            {
                "minItems": lambda v, p: _integer(v, p, minimum=0),
                "maxItems": lambda v, p: _integer(v, p, minimum=0),
            },
        )
        nested = mapping(item["items"], f"{path}.items")
        if "kinds" in nested:
            _codec(nested, f"{path}.items")
        else:
            _codec_kind(nested, f"{path}.items")
    elif kind == "raw":
        _fields(item, path, {"mra_type": _string})
    elif kind in {"time", "date", "date_time"}:
        if "bytes" not in item:
            fail(path, f"{kind} requires bytes")
        allowed = {"time": {2, 3}, "date": {4}, "date_time": {6, 7}}[kind]
        if type(item["bytes"]) is not int or item["bytes"] not in allowed:
            fail(f"{path}.bytes", f"must be one of {sorted(allowed)}")
        if kind == "time":
            _fields(item, path, {"maximum_hour": positive_int})
    elif kind == "text":
        if "bytes" not in item and not {"min_bytes", "max_bytes"} <= item.keys():
            fail(path, "text requires bytes or min_bytes and max_bytes")
        _fields(
            item,
            path,
            {
                "bytes": positive_int,
                "min_bytes": positive_int,
                "max_bytes": positive_int,
                "encoding": _string,
                "bom": _boolean,
                "trim_right": _string_list,
            },
        )
    elif kind == "encoded_text":
        required = {
            "length_byte",
            "encoding_byte",
            "reserved_byte",
            "text_offset",
            "max_text_bytes",
            "encodings",
        }
        if missing := required - item.keys():
            fail(path, f"encoded_text is missing {sorted(missing)!r}")
        for field in required - {"encodings", "max_text_bytes"}:
            _integer(item[field], f"{path}.{field}", minimum=0)
        positive_int(item["max_text_bytes"], f"{path}.max_text_bytes")
        encodings = mapping(item["encodings"], f"{path}.encodings")
        for code, encoding in encodings.items():
            _edt(code, f"{path}.encodings.{code}")
            _string(encoding, f"{path}.encodings.{code}")
    elif kind == "bitmap":
        _fields(item, path, {"size": positive_int, "bits": lambda v, p: mapping(v, p)})


def _codec(value: Any, path: str) -> None:
    codec = mapping(value, path)
    kinds = codec.get("kinds")
    if not isinstance(kinds, list) or not kinds:
        fail(f"{path}.kinds", "must be a non-empty list")
    for index, item in enumerate(kinds):
        _codec_kind(item, f"{path}.kinds[{index}]")
        if item["kind"] == "raw" and index < len(kinds) - 1:
            fail(f"{path}.kinds[{index}]", "raw fallback must be last")


def _boolean(value: Any, path: str) -> None:
    if type(value) is not bool:
        fail(path, "must be a boolean")


def _string_list(value: Any, path: str) -> None:
    if not isinstance(value, list):
        fail(path, "must be a list")
    for index, item in enumerate(value):
        _string(item, f"{path}[{index}]")


def _prometheus(value: Any, path: str) -> None:
    policy = mapping(value, path)
    if "metric_type" in policy and (
        not isinstance(policy["metric_type"], str)
        or policy["metric_type"] not in {"gauge", "counter"}
    ):
        fail(f"{path}.metric_type", "must be gauge or counter")
    export = policy.get("export")
    if export is not None and (
        not isinstance(export, str) or export not in {"enum_map", "raw_uint"}
    ):
        fail(f"{path}.export", "must be enum_map or raw_uint")
    if "enum_map" in policy:
        enum_map = mapping(policy["enum_map"], f"{path}.enum_map")
        if "values" not in enum_map:
            fail(f"{path}.enum_map", "is missing values")
        values = mapping(enum_map["values"], f"{path}.enum_map.values")
        for edt, number in values.items():
            _edt(edt, f"{path}.enum_map.values.{edt}")
            _integer(number, f"{path}.enum_map.values.{edt}")
            if number == -1 or abs(number) > 1 << 53:
                fail(
                    f"{path}.enum_map.values.{edt}",
                    "must be an exact integer other than -1",
                )
        _fields(enum_map, f"{path}.enum_map", {"replace": _boolean})
    if "raw_uint" in policy:
        raw = mapping(policy["raw_uint"], f"{path}.raw_uint")
        if "bytes" not in raw:
            fail(f"{path}.raw_uint", "is missing bytes")
        _integer(raw["bytes"], f"{path}.raw_uint.bytes", minimum=1)
        if raw["bytes"] > 6:
            fail(f"{path}.raw_uint.bytes", "must be at most 6")
    if export == "enum_map" and "enum_map" not in policy:
        fail(path, "enum_map export requires enum_map")
    if export == "raw_uint" and "raw_uint" not in policy:
        fail(path, "raw_uint export requires raw_uint")


def _access(value: Any, path: str) -> None:
    access = mapping(value, path)
    for field in ("get", "set", "inf"):
        if field in access and (
            not isinstance(access[field], str) or access[field] not in ACCESS_VALUES
        ):
            fail(f"{path}.{field}", "must be a supported access value")


def _properties(value: Any, path: str, *, profile: bool) -> None:
    properties = mapping(value, path)
    for epc, raw_property in properties.items():
        property_path = f"{path}.{epc}"
        validate_epc(epc, property_path)
        prop = mapping(raw_property, property_path)
        if not profile and ("name" not in prop or "codec" not in prop):
            fail(property_path, "class property requires name and codec")
        if (
            profile
            and "codec" not in prop
            and any(field in prop for field in ("name", "short_name", "access"))
        ):
            fail(property_path, "profile property requires codec for a replacement")
        _fields(
            prop,
            property_path,
            {
                "name": _localized,
                "short_name": _string,
                "access": _access,
                "codec": _codec,
                "prometheus": _prometheus,
            },
        )
        if profile and "metric_name" in prop:
            name = non_empty_string(prop["metric_name"], f"{property_path}.metric_name")
            if not METRIC_NAME_PATTERN.fullmatch(name):
                fail(f"{property_path}.metric_name", "must be a metric name suffix")


def _match(value: Any, path: str) -> None:
    rules = mapping(value, path)
    required = mapping(rules.get("required", {}), f"{path}.required")
    optional = mapping(rules.get("optional", {}), f"{path}.optional")
    if overlap := required.keys() & optional.keys():
        fail(path, f"EPCs appear in both required and optional: {sorted(overlap)!r}")
    for group in ("required", "optional"):
        if group not in rules:
            continue
        entries = mapping(rules[group], f"{path}.{group}")
        for epc, values in entries.items():
            entry_path = f"{path}.{group}.{epc}"
            validate_epc(epc, entry_path)
            if not isinstance(values, list) or not values:
                fail(entry_path, "must be a non-empty EDT list")
            for index, edt in enumerate(values):
                _edt(edt, f"{entry_path}[{index}]")


def _collection(value: Any, path: str) -> None:
    policy = mapping(value, path)
    _fields(policy, path, {"interval_seconds": positive_int})
    if "batch_size" in policy:
        _integer(policy["batch_size"], f"{path}.batch_size")
        if policy["batch_size"] == 0:
            fail(f"{path}.batch_size", "must be non-zero")
    if "properties" in policy:
        properties = mapping(policy["properties"], f"{path}.properties")
        for epc, raw_property in properties.items():
            entry_path = f"{path}.properties.{epc}"
            validate_epc(epc, entry_path)
            entry = mapping(raw_property, entry_path)
            _fields(entry, entry_path, {"enabled": _boolean})
            if "request" in entry and (
                not isinstance(entry["request"], str)
                or entry["request"] not in {"batch", "single"}
            ):
                fail(f"{entry_path}.request", "must be batch or single")


def validate_catalog(document: Any) -> Mapping[str, Any]:
    """Validate known schema fields without resolving cross-document references."""
    root = mapping(document, "document")
    if type(root.get("catalog_format")) is not int or root["catalog_format"] != 1:
        fail("document.catalog_format", "must be the integer 1")
    for class_code, raw_class in mapping(
        root.get("classes", {}), "document.classes"
    ).items():
        class_path = f"document.classes.{class_code}"
        validate_class_code(class_code, class_path)
        cls = mapping(raw_class, class_path)
        if "kind" in cls and cls["kind"] != "superclass":
            fail(f"{class_path}.kind", "must be superclass")
        _fields(
            cls,
            class_path,
            {"name": _localized, "short_name": _string, "extends": _class_code_list},
        )
        if "properties" in cls:
            _properties(cls["properties"], f"{class_path}.properties", profile=False)
    for profile_id, raw_profile in mapping(
        root.get("profiles", {}), "document.profiles"
    ).items():
        profile_path = f"document.profiles.{profile_id}"
        _string(profile_id, profile_path)
        profile = mapping(raw_profile, profile_path)
        if "class" not in profile and not profile.get("extends"):
            fail(profile_path, "requires class or extends")
        _fields(
            profile,
            profile_path,
            {
                "class": validate_class_code,
                "extends": _profile_id_list,
                "name": _localized,
                "match": _match,
                "collection": _collection,
            },
        )
        if "properties" in profile:
            _properties(
                profile["properties"], f"{profile_path}.properties", profile=True
            )
    return root


def load_and_validate(path: Path = DEFAULT_INPUT) -> Mapping[str, Any]:
    """Read and validate one catalog YAML document."""
    try:
        with path.open(encoding="utf-8") as stream:
            document = yaml.load(stream, Loader=_UniqueKeyLoader)
    except (OSError, yaml.YAMLError) as error:
        raise ValidationError(f"cannot read {path}: {error}") from error
    return validate_catalog(document)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("path", nargs="?", type=Path, default=DEFAULT_INPUT)
    args = parser.parse_args()
    try:
        catalog = load_and_validate(args.path)
    except ValidationError as error:
        print(f"error: {error}")
        return 1
    print(
        f"Validated {args.path}: {len(catalog.get('classes', {}))} classes, "
        f"{len(catalog.get('profiles', {}))} profiles."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
