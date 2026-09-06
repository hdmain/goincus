#!/usr/bin/env python3
"""List all instances.

Usage:
  export GOINCUS_API_KEY=gic_...
  python list.py
"""

from __future__ import annotations

from common import pretty, request


def main() -> None:
    pretty(request("GET", "/api/v1/instances"))


if __name__ == "__main__":
    main()
