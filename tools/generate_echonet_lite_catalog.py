#!/usr/bin/env python3
"""Generate the schema-v1 Echoview system catalog from MRA JSON sources."""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any, Mapping

import yaml

from catalog_overrides import apply_override_patches, build_override_patches
from mra_catalog_compiler import compile_mra_catalog
from validate_echonet_lite_prometheus_metric_type_overrides import (
    load_and_validate as load_prometheus_metric_type_overrides_document,
)
from validate_echonet_lite_prometheus_overrides import (
    load_and_validate as load_prometheus_overrides_document,
)
from validate_echonet_lite_text_codec_overrides import (
    load_and_validate as load_text_codec_overrides_document,
)

PROJECT_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_OUTPUT = PROJECT_ROOT / "catalog" / "echonet_lite_catalog.yaml"
DEFAULT_TEXT_CODEC_OVERRIDES = PROJECT_ROOT / "echonet_lite_text_codec_overrides.yaml"
DEFAULT_PROMETHEUS_OVERRIDES = PROJECT_ROOT / "echonet_lite_prometheus_overrides.yaml"
DEFAULT_PROMETHEUS_METRIC_TYPE_OVERRIDES = (
    PROJECT_ROOT / "echonet_lite_prometheus_metric_type_overrides.yaml"
)


def _load_json_document(path: Path) -> Mapping[str, Any]:
    """Load one MRA JSON document and require an object at its root."""
    document = json.loads(path.read_text())
    if not isinstance(document, Mapping):
        raise ValueError(f"MRA JSON document {path} must be an object")
    return document


def _load_mra_catalog_documents(
    mra_root: Path,
) -> tuple[
    Mapping[str, Any],
    Mapping[str, Mapping[str, Any]],
    Mapping[str, Any],
    list[tuple[Path, bool, Mapping[str, Any]]],
]:
    """Read the MRA source tree without compiling its catalog representation."""
    metadata_document = _load_json_document(mra_root / "metaData.json")
    metadata = metadata_document.get("metaData")
    if not isinstance(metadata, Mapping):
        raise ValueError("MRA metaData.json must contain a metaData object")

    definitions_document = _load_json_document(
        mra_root / "definitions" / "definitions.json"
    )
    definitions = definitions_document.get("definitions")
    if not isinstance(definitions, Mapping):
        raise ValueError("MRA definitions must be a mapping")

    superclass_document = _load_json_document(mra_root / "superClass" / "0x0000.json")
    class_documents: list[tuple[Path, bool, Mapping[str, Any]]] = []
    for directory in ("devices", "nodeProfile"):
        for path in sorted((mra_root / directory).glob("*.json")):
            class_documents.append(
                (path, directory == "devices", _load_json_document(path))
            )
    return metadata, definitions, superclass_document, class_documents


def _apply_validated_overrides(
    classes: Mapping[str, dict[str, Any]],
    document: Mapping[str, Any],
    *,
    allowed_fields: frozenset[str],
) -> None:
    """Apply one validated override document, restricted to its catalog fields."""
    apply_override_patches(
        classes,
        build_override_patches(document, allowed_fields=allowed_fields),
        allowed_fields=allowed_fields,
    )


def _apply_metric_type_overrides(
    classes: Mapping[str, dict[str, Any]], document: Mapping[str, Any]
) -> None:
    """Merge reviewed metric types without replacing an existing export policy."""
    patches = build_override_patches(document, allowed_fields=frozenset({"prometheus"}))
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
                target = target_properties[epc]
            except KeyError as error:
                raise ValueError(
                    f"Override property {class_code}/{epc} is not defined directly by MRA"
                ) from error
            if not isinstance(target, dict):
                raise ValueError(f"Invalid generated property {class_code}/{epc}")
            current = target.get("prometheus", {})
            if not isinstance(current, dict):
                raise ValueError(
                    f"Invalid generated Prometheus policy for {class_code}/{epc}"
                )
            current.update(patch["prometheus"])
            target["prometheus"] = current


def load_system_catalog(
    mra_root: Path,
    text_codec_overrides: Path = DEFAULT_TEXT_CODEC_OVERRIDES,
    prometheus_overrides: Path = DEFAULT_PROMETHEUS_OVERRIDES,
    prometheus_metric_type_overrides: Path = DEFAULT_PROMETHEUS_METRIC_TYPE_OVERRIDES,
) -> dict[str, Any]:
    """Compile a complete schema-v1 system catalog from one MRA release."""
    metadata, definitions, superclass_document, class_documents = (
        _load_mra_catalog_documents(mra_root)
    )
    catalog = compile_mra_catalog(
        metadata, definitions, superclass_document, class_documents
    )
    classes = catalog["classes"]
    if not isinstance(classes, dict):
        raise ValueError("Compiled MRA catalog classes must be a mapping")

    _apply_validated_overrides(
        classes,
        load_text_codec_overrides_document(text_codec_overrides),
        allowed_fields=frozenset({"codec"}),
    )
    _apply_validated_overrides(
        classes,
        load_prometheus_overrides_document(prometheus_overrides),
        allowed_fields=frozenset({"prometheus"}),
    )
    metric_type_overrides = load_prometheus_metric_type_overrides_document(
        prometheus_metric_type_overrides
    )
    source = metric_type_overrides["source"]["mra"]
    if (
        source["release"] != metadata["release"]
        or source["data_version"] != metadata["dataVersion"]
    ):
        raise ValueError(
            "Prometheus metric-type overrides do not match the MRA release"
        )
    _apply_metric_type_overrides(classes, metric_type_overrides)
    return catalog


def render_yaml(catalog: Mapping[str, Any]) -> str:
    """Render deterministic YAML with the project's PyYAML dependency."""
    return yaml.safe_dump(
        dict(catalog), allow_unicode=True, default_flow_style=False, sort_keys=False
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--mra-root",
        type=Path,
        required=True,
        help="Path to the MRA release directory to compile.",
    )
    parser.add_argument(
        "--text-codec-overrides", type=Path, default=DEFAULT_TEXT_CODEC_OVERRIDES
    )
    parser.add_argument(
        "--prometheus-overrides", type=Path, default=DEFAULT_PROMETHEUS_OVERRIDES
    )
    parser.add_argument(
        "--prometheus-metric-type-overrides",
        type=Path,
        default=DEFAULT_PROMETHEUS_METRIC_TYPE_OVERRIDES,
    )
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument(
        "--check",
        action="store_true",
        help="Fail if the generated catalog differs from --output.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    rendered = render_yaml(
        load_system_catalog(
            args.mra_root,
            args.text_codec_overrides,
            args.prometheus_overrides,
            args.prometheus_metric_type_overrides,
        )
    )

    if args.check:
        if not args.output.exists() or args.output.read_text() != rendered:
            print(f"{args.output} is out of date; run {Path(__file__).name}")
            return 1
        return 0

    args.output.write_text(rendered)
    print(f"Wrote {args.output}.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
