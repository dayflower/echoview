"""Pure MRA-to-catalog compilation helpers."""

from __future__ import annotations

import re
from decimal import Decimal
from pathlib import Path
from typing import Any, Mapping, Sequence

# MRA v1.4.0 labels the illuminance sensor as 0x00D0. Appendix Release R
# rev.4 assigns it to class group 0x00 and class code 0x0D, i.e. 0x000D.
EOJ_CORRECTIONS = {"00D0": "000D"}
DEFINITION_REFERENCE_PREFIX = "#/definitions/"
EOJ_PATTERN = re.compile(r"[0-9A-F]{4}")
INTEGER_FORMAT_PATTERN = re.compile(r"(u?)int(8|16|32)")
PROPERTY_MAP_EPCS = frozenset((0x9D, 0x9E, 0x9F))
INSTANCE_LIST_EPCS = frozenset((0xD5, 0xD6))


def _normalized_eoj(value: str) -> str:
    """Return a four-digit class EOJ, applying documented source corrections."""
    source_eoj = value.removeprefix("0x").upper()
    if not EOJ_PATTERN.fullmatch(source_eoj):
        raise ValueError(f"Invalid MRA class EOJ {value!r}")
    return EOJ_CORRECTIONS.get(source_eoj, source_eoj)


def _release_rank(release: str) -> int:
    return ord(release.upper()) - ord("A")


def _applies_to_release(valid_release: Mapping[str, str], release: str) -> bool:
    current = _release_rank(release)
    first = _release_rank(valid_release.get("from", "A"))
    last_name = valid_release.get("to", "latest")
    last = 99 if last_name == "latest" else _release_rank(last_name)
    return first <= current <= last


def _names(value: Mapping[str, Any]) -> dict[str, str]:
    """Keep only the catalog's supported localized names."""
    return {
        language: name
        for language in ("en", "ja")
        if isinstance((name := value.get(language)), str)
    }


def _resolve_definition(
    data: Mapping[str, Any], definitions: Mapping[str, Mapping[str, Any]]
) -> Mapping[str, Any]:
    """Resolve the direct definition references emitted by the MRA files."""
    reference = data.get("$ref")
    if not isinstance(reference, str) or not reference.startswith(
        DEFINITION_REFERENCE_PREFIX
    ):
        return data
    definition_name = reference.removeprefix(DEFINITION_REFERENCE_PREFIX)
    try:
        return definitions[definition_name]
    except KeyError as error:
        raise ValueError(f"Unknown MRA definition reference {reference!r}") from error


def _decimal_text(value: int | float | str) -> str:
    """Render an exact decimal scalar without writing a YAML float."""
    decimal = Decimal(str(value))
    return format(decimal.normalize(), "f")


def _deduplicate_kinds(kinds: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Keep the first occurrence of each equivalent codec kind."""
    unique: list[dict[str, Any]] = []
    for kind in kinds:
        if kind not in unique:
            unique.append(kind)
    return unique


def _compile_kinds(
    data: Mapping[str, Any], definitions: Mapping[str, Mapping[str, Any]]
) -> list[dict[str, Any]]:
    """Compile an MRA value description into one or more catalog codec kinds."""
    resolved = _resolve_definition(data, definitions)
    if resolved is not data:
        return _compile_kinds(resolved, definitions)

    alternatives = resolved.get("oneOf")
    if isinstance(alternatives, list):
        kinds: list[dict[str, Any]] = []
        for alternative in alternatives:
            if not isinstance(alternative, Mapping):
                raise ValueError(f"Invalid MRA oneOf alternative {alternative!r}")
            kinds.extend(_compile_kinds(alternative, definitions))
        if kinds:
            ordered_kinds = [
                *[kind for kind in kinds if kind.get("kind") != "raw"],
                *[kind for kind in kinds if kind.get("kind") == "raw"],
            ]
            return _deduplicate_kinds(ordered_kinds)

    data_type = resolved.get("type")

    if data_type == "state":
        kind: dict[str, Any] = {"kind": "enum"}
        size = resolved.get("size")
        if isinstance(size, int) and size > 0:
            kind["size"] = size

        values: dict[str, dict[str, Any]] = {}
        for member in resolved.get("enum", []):
            if not isinstance(member, Mapping):
                continue
            edt = member.get("edt")
            if not isinstance(edt, str) or not re.fullmatch(
                r"0x(?:[0-9A-Fa-f]{2})+", edt
            ):
                continue
            entry: dict[str, Any] = {}
            if "name" in member:
                entry["value"] = member["name"]
            labels = member.get("descriptions")
            if isinstance(labels, Mapping):
                label = _names(labels)
                if label:
                    entry["label"] = label
            values[f"0x{edt.removeprefix('0x').upper()}"] = entry
        kind["values"] = dict(sorted(values.items()))
        return [kind]

    if data_type == "number":
        data_format = resolved.get("format")
        if isinstance(data_format, str) and (
            match := INTEGER_FORMAT_PATTERN.fullmatch(data_format)
        ):
            unsigned_marker, bits_text = match.groups()
            kind = {
                "kind": "uint" if unsigned_marker == "u" else "int",
                "bytes": int(bits_text) // 8,
            }
            multiple = resolved.get("multiple")
            if isinstance(multiple, (int, float, str)) and multiple != 1:
                kind["scale"] = _decimal_text(multiple)
            unit = resolved.get("unit")
            if isinstance(unit, str):
                kind["unit"] = unit
            for field in ("minimum", "maximum"):
                value = resolved.get(field)
                if isinstance(value, (int, float, str)):
                    kind[field] = _decimal_text(value)
            return [kind]

    if data_type == "array":
        item_size = resolved.get("itemSize")
        items = resolved.get("items")
        if isinstance(item_size, int) and item_size > 0 and isinstance(items, Mapping):
            item_kinds = _compile_kinds(items, definitions)
            if item_kinds:
                kind = {
                    "kind": "array",
                    "itemSize": item_size,
                    "items": {"kinds": item_kinds},
                }
                minimum = resolved.get("minItems")
                maximum = resolved.get("maxItems")
                if isinstance(minimum, int) and minimum >= 0:
                    kind["minItems"] = minimum
                if isinstance(maximum, int) and maximum >= 0:
                    kind["maxItems"] = maximum
                return [kind]

    if data_type == "time":
        size = resolved.get("size", 3)
        if isinstance(size, int) and size in (2, 3):
            kind = {"kind": "time", "bytes": size}
            maximum_of_hour = resolved.get("maximumOfHour", 23)
            if isinstance(maximum_of_hour, int) and 0 <= maximum_of_hour <= 255:
                kind["maximum_hour"] = maximum_of_hour
            return [kind]

    if data_type == "date":
        return [{"kind": "date", "bytes": 4}]

    if data_type == "date-time":
        # The named MRA definition is six bytes (through minutes). The only
        # release-R direct bare date-time value is 0x0279/0xB1; Appendix
        # Release R rev.4 specifies its seven-byte value through seconds.
        size = resolved.get("size", 7)
        if isinstance(size, int) and size in (6, 7):
            return [{"kind": "date_time", "bytes": size}]

    kind = {"kind": "raw"}
    if isinstance(data_type, str) and data_type != "raw":
        kind["mra_type"] = data_type
    return [kind]


def _compile_codec(
    code: str,
    epc: int,
    data: Mapping[str, Any],
    definitions: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    """Compile an MRA value description into a schema-v1 catalog codec."""
    if epc in PROPERTY_MAP_EPCS:
        return {"kinds": [{"kind": "property_map"}]}
    if code == "0EF0" and epc in INSTANCE_LIST_EPCS:
        return {"kinds": [{"kind": "instance_list"}]}
    return {"kinds": _compile_kinds(data, definitions)}


def _raw_edt(value: Any) -> str | None:
    """Return one normalized complete EDT from an MRA enum member."""
    if not isinstance(value, str) or not re.fullmatch(r"0x(?:[0-9A-Fa-f]{2})+", value):
        return None
    return f"0x{value.removeprefix('0x').upper()}"


def _state_prometheus_policy(data: Mapping[str, Any]) -> dict[str, Any] | None:
    """Compile a safe metric policy for one fixed-width MRA state value."""
    members = data.get("enum")
    if not isinstance(members, list) or not members:
        return None
    entries: list[tuple[str, Any]] = []
    for member in members:
        if (
            not isinstance(member, Mapping)
            or (edt := _raw_edt(member.get("edt"))) is None
        ):
            return None
        entries.append((edt, member.get("name")))

    def semantic_integer(value: Any) -> int | None:
        # MRA serializes state ``name`` values as JSON strings, including
        # booleans and integers. Do not treat arbitrary strings as numeric.
        if isinstance(value, bool):
            return int(value)
        if isinstance(value, int):
            return value
        if value == "true":
            return 1
        if value == "false":
            return 0
        if isinstance(value, str) and re.fullmatch(r"-?(?:0|[1-9][0-9]*)", value):
            return int(value)
        return None

    semantic_values = [(edt, semantic_integer(value)) for edt, value in entries]
    if all(value is not None and value != -1 for _, value in semantic_values):
        return {
            "export": "enum_map",
            "enum_map": {"values": {edt: value for edt, value in semantic_values}},
        }

    # Names such as "auto" and "cooling" have no portable semantic integer,
    # but their fixed-width state code is still useful as an opt-in raw gauge.
    size = data.get("size")
    widths = {len(edt.removeprefix("0x")) // 2 for edt, _ in entries}
    if (
        all(
            isinstance(value, str) and semantic_integer(value) is None
            for _, value in entries
        )
        and isinstance(size, int)
        and 1 <= size <= 6
        and widths == {size}
    ):
        return {"export": "raw_uint", "raw_uint": {"bytes": size}}
    return None


def _compile_prometheus_policy(
    data: Mapping[str, Any], definitions: Mapping[str, Mapping[str, Any]]
) -> dict[str, Any] | None:
    """Derive only MRA policies whose numeric meaning is unambiguous."""
    resolved = _resolve_definition(data, definitions)
    if resolved is not data:
        return _compile_prometheus_policy(resolved, definitions)
    alternatives = resolved.get("oneOf")
    if isinstance(alternatives, list):
        policies: list[dict[str, Any]] = []
        for alternative in alternatives:
            if not isinstance(alternative, Mapping):
                return None
            if policy := _compile_prometheus_policy(alternative, definitions):
                if policy not in policies:
                    policies.append(policy)
        return policies[0] if len(policies) == 1 else None
    if resolved.get("type") == "state":
        return _state_prometheus_policy(resolved)
    return None


def _compile_property(
    code: str,
    item: Mapping[str, Any],
    definitions: Mapping[str, Mapping[str, Any]],
) -> tuple[int, dict[str, Any]]:
    epc_text = item.get("epc")
    if not isinstance(epc_text, str) or not re.fullmatch(r"0x[0-9A-Fa-f]{2}", epc_text):
        raise ValueError(f"Invalid MRA EPC {epc_text!r} for class {code}")
    epc = int(epc_text, 16)

    names = item.get("propertyName")
    if not isinstance(names, Mapping) or not (name := _names(names)):
        raise ValueError(f"Missing property name for {code} EPC 0x{epc:02X}")

    property_definition: dict[str, Any] = {"name": name}
    short_name = item.get("shortName")
    if isinstance(short_name, str):
        property_definition["short_name"] = short_name

    access_rule = item.get("accessRule")
    if isinstance(access_rule, Mapping):
        access = {
            operation: value
            for operation in ("get", "set", "inf")
            if isinstance((value := access_rule.get(operation)), str)
        }
        if access:
            property_definition["access"] = access

    data = item.get("data")
    data = data if isinstance(data, Mapping) else {}
    property_definition["codec"] = _compile_codec(code, epc, data, definitions)
    if policy := _compile_prometheus_policy(data, definitions):
        property_definition["prometheus"] = policy
    return epc, property_definition


def _compile_properties(
    code: str,
    document: Mapping[str, Any],
    release: str,
    definitions: Mapping[str, Mapping[str, Any]],
) -> dict[str, dict[str, Any]]:
    """Select release-applicable, class-local MRA properties by EPC."""
    properties: dict[int, dict[str, Any]] = {}
    items = document.get("elProperties")
    if not isinstance(items, list):
        raise ValueError(f"Missing elProperties for MRA class {code}")
    for item in items:
        if not isinstance(item, Mapping):
            raise ValueError(f"Invalid property definition in MRA class {code}")
        valid_release = item.get("validRelease", {})
        if not isinstance(valid_release, Mapping):
            raise ValueError(f"Invalid validRelease in MRA class {code}")
        if not _applies_to_release(valid_release, release):
            continue
        epc, definition = _compile_property(code, item, definitions)
        if epc in properties:
            raise ValueError(
                f"Multiple definitions apply to MRA class {code} EPC 0x{epc:02X}"
            )
        properties[epc] = definition
    return {f"0x{epc:02X}": properties[epc] for epc in sorted(properties)}


def _compile_class(
    code: str,
    document: Mapping[str, Any],
    release: str,
    definitions: Mapping[str, Mapping[str, Any]],
    *,
    superclass: bool = False,
    device: bool = False,
) -> dict[str, Any]:
    names = document.get("className")
    if not isinstance(names, Mapping) or not (name := _names(names)):
        raise ValueError(f"Missing class name for MRA class {code}")

    result: dict[str, Any] = {}
    if superclass:
        result["kind"] = "superclass"
    result["name"] = name
    short_name = document.get("shortName")
    if isinstance(short_name, str):
        result["short_name"] = short_name
    if device:
        result["extends"] = ["0x0000"]
    result["properties"] = _compile_properties(code, document, release, definitions)
    return result


def compile_mra_catalog(
    metadata: Mapping[str, Any],
    definitions: Mapping[str, Mapping[str, Any]],
    superclass_document: Mapping[str, Any],
    class_documents: Sequence[tuple[Path, bool, Mapping[str, Any]]],
) -> dict[str, Any]:
    """Compile parsed MRA documents without reading files or applying overrides."""
    release = metadata.get("release")
    data_version = metadata.get("dataVersion")
    if not isinstance(release, str) or not isinstance(data_version, str):
        raise ValueError(
            "MRA metaData must contain string release and dataVersion values"
        )

    classes: dict[str, dict[str, Any]] = {
        "0x0000": _compile_class(
            "0000", superclass_document, release, definitions, superclass=True
        )
    }
    for path, device, document in class_documents:
        source_eoj = document.get("eoj")
        if not isinstance(source_eoj, str):
            raise ValueError(f"Missing EOJ in {path}")
        code = _normalized_eoj(source_eoj)
        if code == "0000" or f"0x{code}" in classes:
            raise ValueError(f"Duplicate MRA class EOJ 0x{code} in {path}")
        classes[f"0x{code}"] = _compile_class(
            code, document, release, definitions, device=device
        )

    return {
        "catalog_format": 1,
        "source": {"mra": {"data_version": data_version, "release": release}},
        "classes": dict(sorted(classes.items())),
        "profiles": {},
    }
