"""Shared validation primitives for catalog override documents."""

from __future__ import annotations

import re
from pathlib import Path
from typing import Any, Mapping

CLASS_CODE_PATTERN = re.compile(r"0x[0-9A-F]{4}")
EPC_PATTERN = re.compile(r"0x[0-9A-F]{2}")


class ValidationError(ValueError):
    """Raised when an overrides document does not match the supported schema."""


def fail(path: str, message: str) -> None:
    """Raise a validation error scoped to one document path."""
    raise ValidationError(f"{path}: {message}")


def mapping(value: Any, path: str) -> Mapping[str, Any]:
    """Return a mapping with string keys, or raise ValidationError."""
    if not isinstance(value, Mapping):
        fail(path, "must be a mapping")
    if not all(isinstance(key, str) for key in value):
        fail(path, "must have string keys")
    return value


def keys(
    value: Mapping[str, Any], path: str, *, required: set[str], allowed: set[str]
) -> None:
    """Validate required and supported mapping keys."""
    missing = required - value.keys()
    if missing:
        fail(path, f"is missing required keys {sorted(missing)!r}")
    unknown = {key for key in value if key not in allowed and not key.startswith("x-")}
    if unknown:
        fail(path, f"has unsupported keys {sorted(unknown)!r}")


def non_empty_string(value: Any, path: str) -> str:
    """Return a non-empty string, or raise ValidationError."""
    if not isinstance(value, str) or not value:
        fail(path, "must be a non-empty string")
    return value


def positive_int(value: Any, path: str) -> int:
    """Return a positive integer, excluding booleans, or raise ValidationError."""
    if type(value) is not int or value <= 0:
        fail(path, "must be a positive integer")
    return value


def validate_class_code(value: Any, path: str) -> str:
    """Validate one uppercase 0xNNNN class code."""
    if not isinstance(value, str) or not CLASS_CODE_PATTERN.fullmatch(value):
        fail(path, "must use an uppercase 0xNNNN key")
    return value


def validate_epc(value: Any, path: str) -> str:
    """Validate one uppercase 0xNN EPC."""
    if not isinstance(value, str) or not EPC_PATTERN.fullmatch(value):
        fail(path, "must use an uppercase 0xNN key")
    return value


def validate_appendix_reference(value: Any, path: str) -> None:
    """Validate an Appendix document and release reference."""
    appendix = mapping(value, path)
    keys(
        appendix,
        path,
        required={"document", "release"},
        allowed={"document", "release"},
    )
    document_name = non_empty_string(appendix["document"], f"{path}.document")
    if Path(document_name).name != document_name:
        fail(f"{path}.document", "must be a filename, not a path")
    non_empty_string(appendix["release"], f"{path}.release")


def validate_appendix_source(value: Any, path: str) -> None:
    """Validate one property-level Appendix source citation."""
    source = mapping(value, path)
    keys(
        source,
        path,
        required={"document", "pages", "summary"},
        allowed={"document", "pages", "summary"},
    )
    document_name = non_empty_string(source["document"], f"{path}.document")
    if Path(document_name).name != document_name:
        fail(f"{path}.document", "must be a filename, not a path")
    pages = source["pages"]
    if not isinstance(pages, list) or not pages:
        fail(f"{path}.pages", "must be a non-empty list of positive integers")
    for index, page in enumerate(pages):
        positive_int(page, f"{path}.pages[{index}]")
    if len(pages) != len(set(pages)):
        fail(f"{path}.pages", "must not contain duplicate page numbers")
    non_empty_string(source["summary"], f"{path}.summary")
