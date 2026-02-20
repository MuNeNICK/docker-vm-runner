#!/usr/bin/env python3
"""Validate distros.yaml: schema correctness and URL reachability."""

from __future__ import annotations

import re
import sys
from pathlib import Path

import requests
import yaml

DISTROS_PATH = Path(__file__).resolve().parents[2] / "distros.yaml"
VALID_ARCHES = {"x86_64", "aarch64"}
URL_RE = re.compile(r"^https?://")
REQUEST_TIMEOUT = 30
USER_AGENT = "docker-vm-runner/distro-validator (GitHub Actions)"


def load_distros(path: Path) -> dict:
    with open(path) as f:
        return yaml.safe_load(f)


# ── Phase 1: Schema validation (fail-fast) ──────────────────────────


def validate_schema(data: dict) -> list[str]:
    errors: list[str] = []

    if "distributions" not in data:
        errors.append("Top-level 'distributions' key is missing")
        return errors

    distros = data["distributions"]
    if not isinstance(distros, dict):
        errors.append("'distributions' must be a mapping")
        return errors

    for key, entry in distros.items():
        if not isinstance(entry, dict):
            errors.append(f"[{key}] entry is not a mapping")
            continue

        # name
        if "name" not in entry:
            errors.append(f"[{key}] missing required field 'name'")
        elif not isinstance(entry["name"], str):
            errors.append(f"[{key}] 'name' must be a string")

        # url
        if "url" not in entry:
            errors.append(f"[{key}] missing required field 'url'")
        elif not isinstance(entry["url"], str):
            errors.append(f"[{key}] 'url' must be a string")
        elif not URL_RE.match(entry["url"]):
            errors.append(f"[{key}] 'url' must start with http:// or https://")

        # arch
        if "arch" not in entry:
            errors.append(f"[{key}] missing required field 'arch'")
        elif entry["arch"] not in VALID_ARCHES:
            errors.append(
                f"[{key}] 'arch' must be one of {VALID_ARCHES}, got '{entry['arch']}'"
            )

    return errors


# ── Phase 2: URL reachability (collect-all) ──────────────────────────


def check_url(key: str, url: str) -> str | None:
    """Return an error string if the URL is unreachable, else None."""
    session = requests.Session()
    session.headers["User-Agent"] = USER_AGENT

    try:
        resp = session.head(url, timeout=REQUEST_TIMEOUT, allow_redirects=True)
        if resp.status_code < 400:
            return None
        # Some servers reject HEAD; fall back to GET with streaming
        if resp.status_code in (403, 405):
            resp = session.get(
                url, timeout=REQUEST_TIMEOUT, allow_redirects=True, stream=True
            )
            resp.close()
            if resp.status_code < 400:
                return None
        return f"[{key}] HTTP {resp.status_code} for {url}"
    except requests.RequestException as exc:
        return f"[{key}] {exc.__class__.__name__}: {exc} for {url}"


def validate_urls(data: dict) -> list[str]:
    errors: list[str] = []
    distros = data["distributions"]

    for key, entry in distros.items():
        url = entry.get("url")
        if not url:
            continue
        err = check_url(key, url)
        if err:
            errors.append(err)

    return errors


# ── Main ─────────────────────────────────────────────────────────────


def main() -> int:
    print(f"Loading {DISTROS_PATH}")
    data = load_distros(DISTROS_PATH)

    # Phase 1
    print("\n=== Phase 1: Schema validation ===")
    schema_errors = validate_schema(data)
    if schema_errors:
        for e in schema_errors:
            print(f"  ERROR: {e}")
        print(f"\nSchema validation failed with {len(schema_errors)} error(s)")
        return 1
    distro_count = len(data["distributions"])
    print(f"  OK: {distro_count} distributions, all schemas valid")

    # Phase 2
    print("\n=== Phase 2: URL reachability ===")
    url_errors = validate_urls(data)
    if url_errors:
        for e in url_errors:
            print(f"  ERROR: {e}")
        print(f"\nURL validation failed: {len(url_errors)}/{distro_count} unreachable")
        return 1
    print(f"  OK: all {distro_count} URLs reachable")

    print("\nAll checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
