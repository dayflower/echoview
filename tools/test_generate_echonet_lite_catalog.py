"""Tests for the MRA-to-catalog generator."""

from __future__ import annotations

import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest.mock import patch

import yaml

TOOLS_ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(TOOLS_ROOT))

from generate_echonet_lite_catalog import (
    _load_mra_catalog_documents,
    load_system_catalog,
    main,
    parse_args,
    render_yaml,
)
from generate_echonet_lite_prometheus_metric_type_candidates import (
    is_counter_candidate,
)
from mra_catalog_compiler import compile_mra_catalog
from validate_echonet_lite_text_codec_overrides import ValidationError


def write_fixture_mra(mra_root: Path) -> tuple[Path, Path, Path]:
    """Write a minimal MRA release and its reviewed override inputs."""

    def write_json(path: Path, document: object) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(document))

    def property_definition(epc: str, data: object) -> dict[str, object]:
        return {
            "epc": epc,
            "propertyName": {"en": f"Property {epc}"},
            "shortName": f"property{epc.removeprefix('0x')}",
            "accessRule": {"get": "required"},
            "data": data,
        }

    state = {
        "type": "state",
        "size": 1,
        "enum": [
            {"edt": "0x30", "name": "true"},
            {"edt": "0x31", "name": "false"},
        ],
    }
    write_json(
        mra_root / "metaData.json",
        {"metaData": {"release": "R", "dataVersion": "fixture-1"}},
    )
    write_json(
        mra_root / "definitions" / "definitions.json",
        {
            "definitions": {
                "temperature": {
                    "type": "number",
                    "format": "int8",
                    "unit": "Celsius",
                    "minimum": -20,
                    "maximum": 50,
                }
            }
        },
    )
    write_json(
        mra_root / "superClass" / "0x0000.json",
        {
            "eoj": "0x0000",
            "className": {"en": "Superclass"},
            "shortName": "superclass",
            "elProperties": [property_definition("0x80", state)],
        },
    )
    write_json(
        mra_root / "devices" / "0x0130.json",
        {
            "eoj": "0x0130",
            "className": {"en": "Fixture device"},
            "elProperties": [
                property_definition("0xB0", {"$ref": "#/definitions/temperature"}),
                property_definition("0xB1", state),
            ],
        },
    )
    write_json(
        mra_root / "nodeProfile" / "0x0EF0.json",
        {
            "eoj": "0x0EF0",
            "className": {"en": "Node profile"},
            "elProperties": [property_definition("0xD5", {"type": "raw"})],
        },
    )

    text_codec_overrides = mra_root / "text-codec-overrides.yaml"
    text_codec_overrides.write_text(
        yaml.safe_dump(
            {
                "codec_overrides_format": 1,
                "source": {"appendix": {"document": "fixture.pdf", "release": "R"}},
                "selection": {
                    "classes": ["superClass"],
                    "exclude": ["nodeProfile"],
                    "rule": "Overrides fixture text codecs.",
                },
                "classes": {
                    "0x0000": {
                        "properties": {
                            "0x80": {
                                "name": {"en": "Status text"},
                                "codec": {"kinds": [{"kind": "text", "bytes": 4}]},
                                "x-source": {
                                    "appendix": {
                                        "document": "fixture.pdf",
                                        "pages": [1],
                                        "summary": "Defines a four-byte text value.",
                                    }
                                },
                            }
                        }
                    }
                },
            }
        )
    )
    prometheus_overrides = mra_root / "prometheus-overrides.yaml"
    prometheus_overrides.write_text(
        yaml.safe_dump(
            {
                "prometheus_overrides_format": 1,
                "source": {"appendix": {"document": "fixture.pdf", "release": "R"}},
                "classes": {
                    "0x0130": {
                        "properties": {
                            "0xB1": {
                                "prometheus": {
                                    "export": "raw_uint",
                                    "raw_uint": {"bytes": 1},
                                },
                                "x-source": {
                                    "document": "fixture.pdf",
                                    "pages": [1],
                                    "summary": "Overrides fixture policy.",
                                },
                            }
                        }
                    }
                },
            }
        )
    )
    metric_type_overrides = mra_root / "metric-type-overrides.yaml"
    metric_type_overrides.write_text(
        yaml.safe_dump(
            {
                "prometheus_metric_type_overrides_format": 1,
                "source": {
                    "mra": {
                        "release": "R",
                        "data_version": "fixture-1",
                        "classifier": "fixture-v1",
                    }
                },
                "classes": {
                    "0x0130": {
                        "properties": {
                            "0xB0": {"prometheus": {"metric_type": "counter"}}
                        }
                    }
                },
            }
        )
    )
    return text_codec_overrides, prometheus_overrides, metric_type_overrides


class GenerateCatalogTests(unittest.TestCase):
    def test_counter_candidate_classifier_requires_scalar_measured_total(self) -> None:
        candidate = {
            "short_name": "cumulativeElectricEnergy",
            "name": {"en": "Measured cumulative amount of electric energy"},
            "access": {"get": "required"},
            "codec": {"kinds": [{"kind": "uint", "bytes": 4, "unit": "kWh"}]},
        }
        self.assertTrue(is_counter_candidate(candidate))
        for field, value in (
            ("short_name", "cumulativeElectricEnergyLog"),
            ("name", {"en": "Cumulative maximum electric power demand"}),
        ):
            with self.subTest(field=field):
                rejected = dict(candidate)
                rejected[field] = value
                self.assertFalse(is_counter_candidate(rejected))

    def test_requires_mra_root(self) -> None:
        with patch("sys.argv", ["generate_echonet_lite_catalog.py"]):
            with redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as error:
                    parse_args()
        self.assertEqual(error.exception.code, 2)

    def test_generated_catalog_matches_source_and_schema_shape(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            mra_root = Path(temporary_directory)
            text_codec_overrides, prometheus_overrides, metric_type_overrides = (
                write_fixture_mra(mra_root)
            )
            catalog = load_system_catalog(
                mra_root,
                text_codec_overrides,
                prometheus_overrides,
                metric_type_overrides,
            )

        self.assertEqual(catalog["catalog_format"], 1)
        self.assertEqual(
            catalog["source"], {"mra": {"data_version": "fixture-1", "release": "R"}}
        )
        self.assertEqual(catalog["profiles"], {})
        self.assertEqual(set(catalog["classes"]), {"0x0000", "0x0130", "0x0EF0"})

        superclass = catalog["classes"]["0x0000"]
        self.assertEqual(superclass["kind"], "superclass")
        self.assertEqual(
            superclass["properties"]["0x80"]["codec"],
            {"kinds": [{"kind": "text", "bytes": 4}]},
        )
        self.assertEqual(
            superclass["properties"]["0x80"]["name"], {"en": "Property 0x80"}
        )
        self.assertEqual(
            superclass["properties"]["0x80"]["prometheus"],
            {"export": "enum_map", "enum_map": {"values": {"0x30": 1, "0x31": 0}}},
        )

        device = catalog["classes"]["0x0130"]
        self.assertEqual(device["extends"], ["0x0000"])
        self.assertEqual(
            device["properties"]["0xB0"]["codec"],
            {
                "kinds": [
                    {
                        "kind": "int",
                        "bytes": 1,
                        "unit": "Celsius",
                        "minimum": "-20",
                        "maximum": "50",
                    }
                ]
            },
        )
        self.assertEqual(
            device["properties"]["0xB1"]["prometheus"],
            {"export": "raw_uint", "raw_uint": {"bytes": 1}},
        )
        self.assertEqual(
            device["properties"]["0xB0"]["prometheus"], {"metric_type": "counter"}
        )
        self.assertEqual(
            catalog["classes"]["0x0EF0"]["properties"]["0xD5"]["codec"],
            {"kinds": [{"kind": "instance_list"}]},
        )

        rendered = render_yaml(catalog)
        self.assertEqual(yaml.safe_load(rendered), catalog)
        self.assertNotIn("$ref", rendered)

    def test_compiler_transforms_parsed_mra_without_override_or_file_io(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            mra_root = Path(temporary_directory)
            write_fixture_mra(mra_root)
            metadata, definitions, superclass_document, class_documents = (
                _load_mra_catalog_documents(mra_root)
            )
            catalog = compile_mra_catalog(
                metadata, definitions, superclass_document, class_documents
            )

        self.assertEqual(catalog["catalog_format"], 1)
        self.assertEqual(set(catalog["classes"]), {"0x0000", "0x0130", "0x0EF0"})
        self.assertEqual(
            catalog["classes"]["0x0000"]["properties"]["0x80"]["codec"],
            {
                "kinds": [
                    {
                        "kind": "enum",
                        "size": 1,
                        "values": {
                            "0x30": {"value": "true"},
                            "0x31": {"value": "false"},
                        },
                    }
                ]
            },
        )
        self.assertNotIn(
            "prometheus", catalog["classes"]["0x0130"]["properties"]["0xB0"]
        )

    def test_compiler_preserves_codec_boundaries_and_release_selection(self) -> None:
        def property_definition(
            epc: str, data: object, *, valid_release: dict[str, str] | None = None
        ) -> dict[str, object]:
            result: dict[str, object] = {
                "epc": epc,
                "propertyName": {"en": f"Property {epc}"},
                "data": data,
            }
            if valid_release is not None:
                result["validRelease"] = valid_release
            return result

        metadata = {"release": "R", "dataVersion": "fixture-boundaries"}
        superclass = {
            "className": {"en": "Superclass"},
            "elProperties": [],
        }
        device = {
            "eoj": "0x0130",
            "className": {"en": "Fixture device"},
            "elProperties": [
                property_definition(
                    "0x80",
                    {
                        "oneOf": [
                            {"type": "raw"},
                            {"type": "number", "format": "uint8"},
                            {"type": "number", "format": "uint8"},
                        ]
                    },
                ),
                property_definition(
                    "0x81",
                    {
                        "type": "array",
                        "itemSize": 2,
                        "minItems": 1,
                        "maxItems": 3,
                        "items": {"type": "number", "format": "int16"},
                    },
                ),
                property_definition(
                    "0x82", {"type": "time", "size": 2, "maximumOfHour": 24}
                ),
                property_definition("0x83", {"type": "date"}),
                property_definition("0x84", {"type": "date-time", "size": 6}),
                property_definition("0x85", {"type": "bitmap"}),
                property_definition(
                    "0x86", {"type": "raw"}, valid_release={"from": "S"}
                ),
                property_definition("0x87", {"type": "raw"}, valid_release={"to": "Q"}),
                property_definition(
                    "0x88", {"type": "raw"}, valid_release={"from": "R", "to": "R"}
                ),
            ],
        }

        catalog = compile_mra_catalog(
            metadata, {}, superclass, [(Path("fixture.json"), True, device)]
        )
        properties = catalog["classes"]["0x0130"]["properties"]
        expected_kinds = {
            "0x80": [{"kind": "uint", "bytes": 1}, {"kind": "raw"}],
            "0x81": [
                {
                    "kind": "array",
                    "itemSize": 2,
                    "items": {"kinds": [{"kind": "int", "bytes": 2}]},
                    "minItems": 1,
                    "maxItems": 3,
                }
            ],
            "0x82": [{"kind": "time", "bytes": 2, "maximum_hour": 24}],
            "0x83": [{"kind": "date", "bytes": 4}],
            "0x84": [{"kind": "date_time", "bytes": 6}],
            "0x85": [{"kind": "raw", "mra_type": "bitmap"}],
            "0x88": [{"kind": "raw"}],
        }
        for epc, kinds in expected_kinds.items():
            with self.subTest(epc=epc):
                self.assertEqual(properties[epc]["codec"]["kinds"], kinds)
        self.assertNotIn("0x86", properties)
        self.assertNotIn("0x87", properties)

    def test_compiler_rejects_duplicate_epcs_and_unknown_definition_references(
        self,
    ) -> None:
        metadata = {"release": "R", "dataVersion": "fixture-errors"}
        superclass = {
            "className": {"en": "Superclass"},
            "elProperties": [],
        }

        cases = (
            (
                "duplicate EPC",
                [
                    {
                        "epc": "0x80",
                        "propertyName": {"en": "First"},
                        "data": {"type": "raw"},
                    },
                    {
                        "epc": "0x80",
                        "propertyName": {"en": "Second"},
                        "data": {"type": "raw"},
                    },
                ],
                "Multiple definitions apply to MRA class 0130 EPC 0x80",
            ),
            (
                "unknown definition",
                [
                    {
                        "epc": "0x80",
                        "propertyName": {"en": "Unknown"},
                        "data": {"$ref": "#/definitions/missing"},
                    }
                ],
                "Unknown MRA definition reference '#/definitions/missing'",
            ),
        )
        for name, properties, message in cases:
            with self.subTest(name=name):
                device = {
                    "eoj": "0x0130",
                    "className": {"en": "Fixture device"},
                    "elProperties": properties,
                }
                with self.assertRaisesRegex(ValueError, message):
                    compile_mra_catalog(
                        metadata,
                        {},
                        superclass,
                        [(Path("fixture.json"), True, device)],
                    )

    def test_check_detects_stale_output(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary_root = Path(temporary_directory)
            mra_root = temporary_root / "mra"
            text_codec_overrides, prometheus_overrides, metric_type_overrides = (
                write_fixture_mra(mra_root)
            )
            output = temporary_root / "catalog.yaml"
            output.write_text("stale\n")
            arguments = [
                "generate_echonet_lite_catalog.py",
                "--mra-root",
                str(mra_root),
                "--text-codec-overrides",
                str(text_codec_overrides),
                "--prometheus-overrides",
                str(prometheus_overrides),
                "--prometheus-metric-type-overrides",
                str(metric_type_overrides),
                "--output",
                str(output),
                "--check",
            ]
            with patch("sys.argv", arguments), redirect_stdout(io.StringIO()):
                self.assertEqual(main(), 1)

            output.write_text(
                render_yaml(
                    load_system_catalog(
                        mra_root,
                        text_codec_overrides,
                        prometheus_overrides,
                        metric_type_overrides,
                    )
                )
            )
            with patch("sys.argv", arguments), redirect_stdout(io.StringIO()):
                self.assertEqual(main(), 0)

    def test_rejects_text_override_that_bypasses_the_full_schema(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            mra_root = Path(temporary_directory)
            text_codec_overrides, prometheus_overrides, metric_type_overrides = (
                write_fixture_mra(mra_root)
            )
            document = yaml.safe_load(text_codec_overrides.read_text())
            del document["classes"]["0x0000"]["properties"]["0x80"]["x-source"]
            text_codec_overrides.write_text(yaml.safe_dump(document))

            with self.assertRaisesRegex(ValidationError, "missing required keys"):
                load_system_catalog(
                    mra_root,
                    text_codec_overrides,
                    prometheus_overrides,
                    metric_type_overrides,
                )

    def test_override_types_report_identical_missing_catalog_targets(self) -> None:
        cases = (
            ("0x0999", "0x80", "Override class 0x0999 is not in the MRA catalog"),
            (
                "0x0000",
                "0xB2",
                "Override property 0x0000/0xB2 is not defined directly by MRA",
            ),
        )
        for class_code, epc, message in cases:
            with self.subTest(class_code=class_code, epc=epc):
                errors = [
                    self._missing_target_error(override_type, class_code, epc)
                    for override_type in ("text", "prometheus")
                ]
                self.assertEqual([type(error) for error in errors], [ValueError] * 2)
                self.assertEqual([str(error) for error in errors], [message] * 2)

    def _missing_target_error(
        self, override_type: str, class_code: str, epc: str
    ) -> ValueError:
        with tempfile.TemporaryDirectory() as temporary_directory:
            mra_root = Path(temporary_directory)
            text_codec_overrides, prometheus_overrides, metric_type_overrides = (
                write_fixture_mra(mra_root)
            )
            override_path = (
                text_codec_overrides
                if override_type == "text"
                else prometheus_overrides
            )
            document = yaml.safe_load(override_path.read_text())
            source_class = "0x0000" if override_type == "text" else "0x0130"
            source_epc = "0x80" if override_type == "text" else "0xB1"
            property_definition = (
                document["classes"].pop(source_class)["properties"].pop(source_epc)
            )
            document["classes"][class_code] = {"properties": {epc: property_definition}}
            override_path.write_text(yaml.safe_dump(document))

            with self.assertRaises(ValueError) as error:
                load_system_catalog(
                    mra_root,
                    text_codec_overrides,
                    prometheus_overrides,
                    metric_type_overrides,
                )
            return error.exception


if __name__ == "__main__":
    unittest.main()
