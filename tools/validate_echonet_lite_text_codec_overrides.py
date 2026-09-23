#!/usr/bin/env python3
"""Validate the schema of Appendix-derived text codec overrides."""

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
    non_empty_string as _string,
    positive_int as _positive_int,
    validate_appendix_reference,
    validate_appendix_source,
    validate_class_code,
    validate_epc,
)

PROJECT_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_INPUT = PROJECT_ROOT / "echonet_lite_text_codec_overrides.yaml"
ENCODING_CODE_PATTERN = re.compile(r"0x[0-9A-F]{2}")


def _validate_localized_name(value: Any, path: str) -> None:
    name = _mapping(value, path)
    if not name:
        _fail(path, "must not be empty")
    for language, text in name.items():
        _string(language, f"{path} key")
        _string(text, f"{path}.{language}")


def _validate_text_kind(value: Mapping[str, Any], path: str) -> None:
    allowed = {
        "kind",
        "bytes",
        "encoding",
        "min_bytes",
        "max_bytes",
        "bom",
        "trim_right",
    }
    _keys(value, path, required={"kind"}, allowed=allowed)
    has_bytes = "bytes" in value
    has_range = "min_bytes" in value or "max_bytes" in value
    if has_bytes == has_range:
        _fail(path, "must define either bytes or min_bytes/max_bytes")
    if has_bytes:
        _positive_int(value["bytes"], f"{path}.bytes")
    else:
        minimum = (
            _positive_int(value["min_bytes"], f"{path}.min_bytes")
            if "min_bytes" in value
            else None
        )
        maximum = (
            _positive_int(value["max_bytes"], f"{path}.max_bytes")
            if "max_bytes" in value
            else None
        )
        if minimum is not None and maximum is not None and minimum > maximum:
            _fail(path, "must not have min_bytes greater than max_bytes")
    if "encoding" in value:
        _string(value["encoding"], f"{path}.encoding")
    if "bom" in value and type(value["bom"]) is not bool:
        _fail(f"{path}.bom", "must be a boolean")
    if "trim_right" in value:
        trim_right = value["trim_right"]
        if not isinstance(trim_right, list) or not all(
            isinstance(item, str) for item in trim_right
        ):
            _fail(f"{path}.trim_right", "must be a list of strings")
        if set(trim_right) - {"nul", "space"}:
            _fail(f"{path}.trim_right", "supports only 'nul' and 'space'")
        if len(trim_right) != len(set(trim_right)):
            _fail(f"{path}.trim_right", "must not contain duplicate values")


def _validate_encoded_text_kind(value: Mapping[str, Any], path: str) -> None:
    fields = {"length_byte", "encoding_byte", "reserved_byte", "text_offset"}
    allowed = {"kind", *fields, "max_text_bytes", "encodings"}
    _keys(
        value,
        path,
        required={"kind", *fields, "max_text_bytes", "encodings"},
        allowed=allowed,
    )
    offsets: dict[str, int] = {}
    for field in fields:
        offset = value[field]
        if type(offset) is not int or offset < 0:
            _fail(f"{path}.{field}", "must be a non-negative integer")
        offsets[field] = offset
    prefix_offsets = [offsets[field] for field in fields if field != "text_offset"]
    if len(prefix_offsets) != len(set(prefix_offsets)):
        _fail(path, "must use distinct prefix byte offsets")
    if offsets["text_offset"] <= max(prefix_offsets):
        _fail(f"{path}.text_offset", "must follow all prefix byte offsets")
    _positive_int(value["max_text_bytes"], f"{path}.max_text_bytes")
    encodings = _mapping(value["encodings"], f"{path}.encodings")
    if not encodings:
        _fail(f"{path}.encodings", "must not be empty")
    for code, encoding in encodings.items():
        if not ENCODING_CODE_PATTERN.fullmatch(code):
            _fail(f"{path}.encodings key {code!r}", "must be an uppercase 0xNN code")
        _string(encoding, f"{path}.encodings.{code}")


def _validate_codec(value: Any, path: str) -> None:
    codec = _mapping(value, path)
    _keys(codec, path, required={"kinds"}, allowed={"kinds"})
    kinds = codec["kinds"]
    if not isinstance(kinds, list) or not kinds:
        _fail(f"{path}.kinds", "must be a non-empty list")
    for index, raw_kind in enumerate(kinds):
        kind_path = f"{path}.kinds[{index}]"
        kind = _mapping(raw_kind, kind_path)
        kind_name = _string(kind.get("kind"), f"{kind_path}.kind")
        if kind_name == "text":
            _validate_text_kind(kind, kind_path)
        elif kind_name == "encoded_text":
            _validate_encoded_text_kind(kind, kind_path)
        else:
            _fail(f"{kind_path}.kind", "must be 'text' or 'encoded_text'")


def validate_overrides(document: Any) -> None:
    """Raise ValidationError unless document matches the text overrides schema."""
    root = _mapping(document, "document")
    _keys(
        root,
        "document",
        required={"codec_overrides_format", "source", "selection", "classes"},
        allowed={"codec_overrides_format", "source", "selection", "classes"},
    )
    if root["codec_overrides_format"] != 1:
        _fail("document.codec_overrides_format", "must be the integer 1")

    source = _mapping(root["source"], "document.source")
    _keys(source, "document.source", required={"appendix"}, allowed={"appendix"})
    validate_appendix_reference(source["appendix"], "document.source.appendix")

    selection = _mapping(root["selection"], "document.selection")
    _keys(
        selection,
        "document.selection",
        required={"classes", "exclude", "rule"},
        allowed={"classes", "exclude", "rule"},
    )
    for field in ("classes", "exclude"):
        entries = selection[field]
        if (
            not isinstance(entries, list)
            or not entries
            or not all(isinstance(entry, str) and entry for entry in entries)
        ):
            _fail(f"document.selection.{field}", "must be a non-empty list of strings")
    _string(selection["rule"], "document.selection.rule")

    classes = _mapping(root["classes"], "document.classes")
    if not classes:
        _fail("document.classes", "must not be empty")
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
                required={"name", "codec", "x-source"},
                allowed={"name", "codec", "x-source"},
            )
            _validate_localized_name(
                property_definition["name"], f"{property_path}.name"
            )
            _validate_codec(property_definition["codec"], f"{property_path}.codec")
            source_extension = _mapping(
                property_definition["x-source"], f"{property_path}.x-source"
            )
            _keys(
                source_extension,
                f"{property_path}.x-source",
                required={"appendix"},
                allowed={"appendix"},
            )
            validate_appendix_source(
                source_extension["appendix"], f"{property_path}.x-source.appendix"
            )


def load_and_validate(path: Path) -> Mapping[str, Any]:
    """Load an overrides document and validate its schema."""
    try:
        document = yaml.safe_load(path.read_text())
    except (OSError, yaml.YAMLError) as error:
        raise ValidationError(f"cannot read {path}: {error}") from error
    validate_overrides(document)
    return _mapping(document, "document")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, default=DEFAULT_INPUT)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        document = load_and_validate(args.input)
    except ValidationError as error:
        print(f"error: {error}")
        return 1
    property_count = sum(
        len(class_definition["properties"])
        for class_definition in document["classes"].values()
    )
    print(
        f"Validated {args.input}: {len(document['classes'])} classes, {property_count} properties."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
