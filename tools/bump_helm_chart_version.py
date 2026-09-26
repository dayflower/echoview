"""Bump the Echoview Prometheus Helm chart version for a release PR."""

import argparse
import re
from pathlib import Path

CHART_PATH = Path("charts/echoview-prometheus/Chart.yaml")
VERSION_LINE = re.compile(r"^version: (\d+)\.(\d+)\.(\d+)$", re.MULTILINE)
APP_VERSION_LINE = re.compile(r'^appVersion: "([^"\n]+)"$', re.MULTILINE)
IMAGE_TAG = re.compile(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?")


def single_match(pattern: re.Pattern[str], content: str, field: str) -> re.Match[str]:
    matches = list(pattern.finditer(content))
    if len(matches) != 1:
        raise ValueError(f"expected exactly one {field} in Chart.yaml")
    return matches[0]


def bump(content: str, part: str, app_version: str) -> tuple[str, str]:
    version_match = single_match(VERSION_LINE, content, "stable chart version")
    app_match = single_match(APP_VERSION_LINE, content, "quoted appVersion")
    major, minor, patch = (int(value) for value in version_match.groups())
    if part == "patch":
        patch += 1
    elif part == "minor":
        minor += 1
        patch = 0
    elif part == "major":
        major += 1
        minor = 0
        patch = 0
    else:
        raise ValueError(f"unsupported version bump: {part}")

    next_version = f"{major}.{minor}.{patch}"
    if app_version and not IMAGE_TAG.fullmatch(app_version):
        raise ValueError("appVersion must be a vX.Y.Z image tag")
    replacement_app_version = app_version or app_match.group(1)
    updated = VERSION_LINE.sub(f"version: {next_version}", content, count=1)
    updated = APP_VERSION_LINE.sub(
        f'appVersion: "{replacement_app_version}"', updated, count=1
    )
    return updated, next_version


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--chart", type=Path, default=CHART_PATH)
    parser.add_argument("--bump", choices=("patch", "minor", "major"), required=True)
    parser.add_argument("--app-version", default="")
    args = parser.parse_args()

    current = args.chart.read_text()
    updated, version = bump(current, args.bump, args.app_version)
    args.chart.write_text(updated)
    print(version)


if __name__ == "__main__":
    main()
