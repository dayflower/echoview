"""Tests for structural catalog schema validation."""

from __future__ import annotations

import copy
import sys
import tempfile
import unittest
from pathlib import Path

TOOLS_ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(TOOLS_ROOT))

from validate_echonet_lite_catalog import (  # noqa: E402
    ValidationError,
    load_and_validate,
    validate_catalog,
)


def _catalog() -> dict:
    return {
        "catalog_format": 1,
        "classes": {
            "0x0130": {
                "name": {"en": "Air conditioner"},
                "properties": {
                    "0x80": {
                        "name": {"en": "Status"},
                        "access": {"get": "required"},
                        "codec": {"kinds": [{"kind": "raw"}]},
                    }
                },
            }
        },
        "profiles": {
            "acme": {
                "class": "0x0130",
                "extends": ["parent-defined-elsewhere"],
                "properties": {"0x80": {"metric_name": "status"}},
            }
        },
    }


class CatalogValidationTests(unittest.TestCase):
    def test_repository_catalog_matches_structural_schema(self) -> None:
        catalog = load_and_validate()
        self.assertEqual(catalog["catalog_format"], 1)
        self.assertTrue(catalog["classes"])

    def test_accepts_profile_reference_without_resolving_it(self) -> None:
        validate_catalog(_catalog())

    def test_accepts_profile_schema_fields(self) -> None:
        catalog = _catalog()
        profile = catalog["profiles"]["acme"]
        profile["match"] = {"required": {"0x8A": ["0x001122"]}}
        profile["collection"] = {
            "interval_seconds": 60,
            "batch_size": -1,
            "properties": {"0x80": {"enabled": True, "request": "single"}},
        }
        profile["properties"]["0xE1"] = {
            "name": {"en": "Power"},
            "codec": {"kinds": [{"kind": "uint", "bytes": 2, "unit": "W"}]},
            "prometheus": {"metric_type": "counter"},
        }
        validate_catalog(catalog)

    def test_accepts_enum_label_without_value(self) -> None:
        catalog = _catalog()
        catalog["classes"]["0x0130"]["properties"]["0x80"]["codec"] = {
            "kinds": [{"kind": "enum", "values": {"0x30": {"label": {"en": "On"}}}}]
        }
        validate_catalog(catalog)

    def test_rejects_malformed_known_fields(self) -> None:
        cases = [
            ("format", lambda c: c.update(catalog_format=True), "catalog_format"),
            ("classes", lambda c: c.update(classes=[]), "classes"),
            ("class code", lambda c: c["classes"].update({"0x013a": {}}), "0x013a"),
            (
                "epc",
                lambda c: c["classes"]["0x0130"]["properties"].update({"0x8a": {}}),
                "0x8a",
            ),
            (
                "missing codec",
                lambda c: c["classes"]["0x0130"]["properties"]["0x80"].pop("codec"),
                "requires name and codec",
            ),
            (
                "nested kind",
                lambda c: c["classes"]["0x0130"]["properties"]["0x80"].update(
                    codec={
                        "kinds": [
                            {
                                "kind": "array",
                                "itemSize": 1,
                                "items": {"kind": "unknown"},
                            }
                        ]
                    }
                ),
                "supported codec kind",
            ),
            (
                "access",
                lambda c: c["classes"]["0x0130"]["properties"]["0x80"]["access"].update(
                    get=[]
                ),
                "supported access value",
            ),
            (
                "match EDT",
                lambda c: c["profiles"]["acme"].update(
                    match={"required": {"0x8A": ["0xabc"]}}
                ),
                "complete 0x-prefixed EDT",
            ),
            (
                "profile replacement",
                lambda c: c["profiles"]["acme"]["properties"]["0x80"].update(
                    name={"en": "Status"}
                ),
                "requires codec for a replacement",
            ),
            (
                "profile class",
                lambda c: c["profiles"].update(acme={"name": {"en": "ACME"}}),
                "requires class or extends",
            ),
            (
                "match overlap",
                lambda c: c["profiles"]["acme"].update(
                    match={
                        "required": {"0x8A": ["0x001122"]},
                        "optional": {"0x8A": ["0x001122"]},
                    }
                ),
                "both required and optional",
            ),
            (
                "raw order",
                lambda c: c["classes"]["0x0130"]["properties"]["0x80"].update(
                    codec={"kinds": [{"kind": "raw"}, {"kind": "property_map"}]}
                ),
                "raw fallback must be last",
            ),
        ]
        for name, mutate, message in cases:
            with self.subTest(name=name):
                catalog = copy.deepcopy(_catalog())
                mutate(catalog)
                with self.assertRaisesRegex(ValidationError, message):
                    validate_catalog(catalog)

    def test_rejects_duplicate_keys_and_multiple_documents(self) -> None:
        cases = [
            ("catalog_format: 1\ncatalog_format: 1\n", "duplicate key"),
            ("catalog_format: 1\n---\ncatalog_format: 1\n", "single document"),
        ]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "catalog.yaml"
            for contents, message in cases:
                with self.subTest(message=message):
                    path.write_text(contents, encoding="utf-8")
                    with self.assertRaisesRegex(ValidationError, message):
                        load_and_validate(path)


if __name__ == "__main__":
    unittest.main()
