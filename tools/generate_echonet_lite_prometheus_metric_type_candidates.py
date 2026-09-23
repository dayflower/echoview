#!/usr/bin/env python3
"""Generate review candidates for MRA scalar cumulative counters.

The output is intentionally not consumed by the catalog generator. Review its
diff, then copy only approved entries into the metric-type overrides document.
"""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Any, Mapping

import yaml

from generate_echonet_lite_catalog import _load_mra_catalog_documents
from mra_catalog_compiler import compile_mra_catalog

CLASSIFIER = "scalar-measured-cumulative-v1"
EXCLUDED_TERMS = (
    "at every",
    "coefficient",
    "digit",
    "history",
    "log",
    "maximum",
    "reset",
    "setting",
    "unit",
)


def is_counter_candidate(property_definition: Mapping[str, Any]) -> bool:
    """Return whether one compiled MRA property is a reviewable counter candidate."""
    short_name = property_definition.get("short_name")
    names = property_definition.get("name")
    codec = property_definition.get("codec")
    access = property_definition.get("access")
    if not isinstance(short_name, str) or not isinstance(names, Mapping):
        return False
    english_name = names.get("en")
    if not isinstance(english_name, str) or not isinstance(codec, Mapping):
        return False
    if not isinstance(access, Mapping) or access.get("get") == "notApplicable":
        return False
    text = f"{short_name} {english_name}".lower()
    if "cumulative" not in text and "accumulated" not in text:
        return False
    if "measured" not in english_name.lower():
        return False
    if any(term in text for term in EXCLUDED_TERMS):
        return False
    kinds = codec.get("kinds")
    if (
        not isinstance(kinds, list)
        or len(kinds) != 1
        or not isinstance(kinds[0], Mapping)
    ):
        return False
    return kinds[0].get("kind") == "uint" and isinstance(kinds[0].get("unit"), str)


def candidate_document(mra_root: Path) -> dict[str, Any]:
    """Compile MRA documents and return deterministic, unreviewed candidates."""
    metadata, definitions, superclass_document, class_documents = (
        _load_mra_catalog_documents(mra_root)
    )
    catalog = compile_mra_catalog(
        metadata, definitions, superclass_document, class_documents
    )
    classes: dict[str, Any] = {}
    for class_code, class_definition in catalog["classes"].items():
        properties = class_definition["properties"]
        candidates = {
            epc: {"prometheus": {"metric_type": "counter"}}
            for epc, property_definition in properties.items()
            if is_counter_candidate(property_definition)
        }
        if candidates:
            classes[class_code] = {"properties": candidates}
    return {
        "prometheus_metric_type_overrides_format": 1,
        "source": {
            "mra": {
                "release": metadata["release"],
                "data_version": metadata["dataVersion"],
                "classifier": CLASSIFIER,
            }
        },
        "classes": classes,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mra-root", type=Path, required=True)
    parser.add_argument(
        "--output", type=Path, help="Write candidates to this path instead of stdout."
    )
    args = parser.parse_args()
    rendered = yaml.safe_dump(
        candidate_document(args.mra_root),
        allow_unicode=True,
        default_flow_style=False,
        sort_keys=False,
    )
    if args.output is None:
        print(rendered, end="")
    else:
        args.output.write_text(rendered)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
