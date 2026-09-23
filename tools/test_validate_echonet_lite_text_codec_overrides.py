"""Tests for text codec override schema validation."""

from __future__ import annotations

import io
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from copy import deepcopy
from pathlib import Path
from unittest.mock import patch

TOOLS_ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(TOOLS_ROOT))

from validate_echonet_lite_text_codec_overrides import (
    ValidationError,
    load_and_validate,
    main,
    validate_overrides,
)

PROJECT_ROOT = TOOLS_ROOT.parent
OVERRIDES_PATH = PROJECT_ROOT / "echonet_lite_text_codec_overrides.yaml"


class TextCodecOverrideValidationTests(unittest.TestCase):
    def test_repository_overrides_match_the_schema(self) -> None:
        overrides = load_and_validate(OVERRIDES_PATH)
        self.assertEqual(overrides["codec_overrides_format"], 1)

    def test_rejects_invalid_text_bounds(self) -> None:
        overrides = deepcopy(load_and_validate(OVERRIDES_PATH))
        overrides["classes"]["0x0000"]["properties"]["0x8C"]["codec"]["kinds"][0][
            "bytes"
        ] = 0
        with self.assertRaisesRegex(ValidationError, "positive integer"):
            validate_overrides(overrides)

    def test_rejects_invalid_encoded_text_offsets(self) -> None:
        overrides = deepcopy(load_and_validate(OVERRIDES_PATH))
        overrides["classes"]["0x0602"]["properties"]["0xB3"]["codec"]["kinds"][0][
            "text_offset"
        ] = 2
        with self.assertRaisesRegex(
            ValidationError, "must follow all prefix byte offsets"
        ):
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

            with patch("sys.argv", ["validator.py", "--input", str(invalid_path)]):
                with redirect_stdout(io.StringIO()) as stdout:
                    self.assertEqual(main(), 1)
            self.assertIn("error: cannot read", stdout.getvalue())


if __name__ == "__main__":
    unittest.main()
