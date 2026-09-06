#!/usr/bin/env python3
"""Check goincus health (no API key required).

Usage:
  python health.py
"""

from __future__ import annotations

from common import pretty, request


def main() -> None:
    pretty(request("GET", "/api/v1/health", auth=False))


if __name__ == "__main__":
    main()
