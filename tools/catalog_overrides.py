"""Normalize and apply validated catalog override documents."""

from __future__ import annotations

import re
from typing import Any, Mapping

EOJ_PATTERN = re.compile(r"[0-9A-F]{4}")
EPC_PATTERN = re.compile(r"[0-9A-F]{2}")


def _normalized_eoj(value: str) -> str:
    """Return one canonical catalog class code from an override key."""
    normalized = value.removeprefix("0x").upper()
    if not EOJ_PATTERN.fullmatch(normalized):
        raise ValueError(f"Invalid class override {value!r}")
    return f"0x{normalized}"


def _normalized_epc(value: str) -> str:
    """Return one canonical catalog EPC from an override key."""
    normalized = value.removeprefix("0x").upper()
    if not EPC_PATTERN.fullmatch(normalized):
        raise ValueError(f"Invalid EPC override {value!r}")
    return f"0x{normalized}"


def build_override_patches(
    document: Mapping[str, Any], *, allowed_fields: frozenset[str]
) -> dict[str, dict[str, dict[str, Any]]]:
    """Extract allowed fields from a validated override document into patches."""
    raw_classes = document.get("classes")
    if not isinstance(raw_classes, Mapping):
        raise ValueError("Invalid override classes")

    patches: dict[str, dict[str, dict[str, Any]]] = {}
    for class_code, class_definition in raw_classes.items():
        if not isinstance(class_code, str) or not isinstance(class_definition, Mapping):
            raise ValueError("Invalid class override")
        raw_properties = class_definition.get("properties")
        if not isinstance(raw_properties, Mapping):
            raise ValueError(f"Missing properties for class {class_code}")

        properties: dict[str, dict[str, Any]] = {}
        for epc, property_definition in raw_properties.items():
            if not isinstance(epc, str) or not isinstance(property_definition, Mapping):
                raise ValueError("Invalid property override")
            patch: dict[str, Any] = {}
            for field in allowed_fields:
                value = property_definition.get(field)
                if not isinstance(value, Mapping):
                    raise ValueError(f"Invalid {field} override {class_code}/{epc}")
                patch[field] = dict(value)
            properties[_normalized_epc(epc)] = patch
        patches[_normalized_eoj(class_code)] = properties
    return patches


def apply_override_patches(
    classes: Mapping[str, dict[str, Any]],
    patches: Mapping[str, Mapping[str, Mapping[str, Any]]],
    *,
    allowed_fields: frozenset[str],
) -> None:
    """Apply allowed override fields after checking catalog class and EPC presence."""
    for class_code, properties in patches.items():
        try:
            target_properties = classes[class_code]["properties"]
        except KeyError as error:
            raise ValueError(
                f"Override class {class_code} is not in the MRA catalog"
            ) from error
        if not isinstance(target_properties, dict):
            raise ValueError(f"Invalid generated properties for {class_code}")

        for epc, patch in properties.items():
            try:
                target_property = target_properties[epc]
            except KeyError as error:
                raise ValueError(
                    f"Override property {class_code}/{epc} is not defined directly by MRA"
                ) from error
            if not isinstance(target_property, dict):
                raise ValueError(f"Invalid generated property {class_code}/{epc}")

            unsupported_fields = set(patch) - allowed_fields
            if unsupported_fields:
                raise ValueError(
                    f"Override {class_code}/{epc} has unsupported fields "
                    f"{sorted(unsupported_fields)!r}"
                )
            for field, value in patch.items():
                if not isinstance(value, Mapping):
                    raise ValueError(f"Invalid {field} override {class_code}/{epc}")
                target_property[field] = dict(value)
