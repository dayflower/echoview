"""Tests for Prometheus override schema validation."""

from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

TOOLS_ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(TOOLS_ROOT))

from validate_echonet_lite_prometheus_metric_type_overrides import (
    load_and_validate as load_metric_type_overrides,
    validate_overrides as validate_metric_type_overrides,
)
from validate_echonet_lite_prometheus_overrides import (
    ValidationError,
    load_and_validate,
    validate_overrides,
)
from validate_echonet_lite_text_codec_overrides import (
    ValidationError as TextCodecValidationError,
)

PROJECT_ROOT = TOOLS_ROOT.parent
OVERRIDES_PATH = PROJECT_ROOT / "echonet_lite_prometheus_overrides.yaml"


class PrometheusOverrideValidationTests(unittest.TestCase):
    def test_reexports_the_shared_validation_error(self) -> None:
        self.assertIs(ValidationError, TextCodecValidationError)

    def test_repository_overrides_match_the_schema(self) -> None:
        overrides = load_and_validate(OVERRIDES_PATH)
        self.assertEqual(overrides["prometheus_overrides_format"], 1)

    def test_rejects_unsafe_raw_uint_width(self) -> None:
        overrides = {
            "prometheus_overrides_format": 1,
            "source": {"appendix": {"document": "Appendix.pdf", "release": "R"}},
            "classes": {
                "0x0130": {
                    "properties": {
                        "0xB0": {
                            "prometheus": {
                                "export": "raw_uint",
                                "raw_uint": {"bytes": 7},
                            },
                            "x-source": {
                                "document": "Appendix.pdf",
                                "pages": [1],
                                "summary": "Defines an unsigned state code.",
                            },
                        }
                    }
                }
            },
        }
        with self.assertRaisesRegex(ValidationError, "1 through 6"):
            validate_overrides(overrides)

    def test_wraps_file_and_yaml_read_errors_as_validation_errors(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            directory = Path(temporary_directory)
            invalid_path = directory / "invalid.yaml"
            invalid_path.write_text("classes: [\n")
            for path in (directory / "missing.yaml", invalid_path):
                with self.subTest(path=path.name):
                    with self.assertRaisesRegex(ValidationError, "cannot read"):
                        load_and_validate(path)


class PrometheusMetricTypeOverrideValidationTests(unittest.TestCase):
    def test_repository_overrides_match_the_schema(self) -> None:
        overrides = load_metric_type_overrides(
            PROJECT_ROOT / "echonet_lite_prometheus_metric_type_overrides.yaml"
        )
        self.assertEqual(overrides["prometheus_metric_type_overrides_format"], 1)

    def test_rejects_non_counter_type(self) -> None:
        overrides = {
            "prometheus_metric_type_overrides_format": 1,
            "source": {
                "mra": {
                    "release": "R",
                    "data_version": "1.3.2",
                    "classifier": "fixture-v1",
                }
            },
            "classes": {
                "0x0279": {
                    "properties": {"0xE1": {"prometheus": {"metric_type": "gauge"}}}
                }
            },
        }
        with self.assertRaisesRegex(ValidationError, "must be 'counter'"):
            validate_metric_type_overrides(overrides)


if __name__ == "__main__":
    unittest.main()
