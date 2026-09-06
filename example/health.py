#!/usr/bin/env python3
"""Check goincus health (API key accepted but not required by the server).

Usage:
  python health.py <url> [api-key]
"""

from __future__ import annotations

import sys

from common import Client, pretty


def main() -> None:
    if len(sys.argv) < 2:
        print("usage: health.py <url> [api-key]", file=sys.stderr)
        sys.exit(2)
    key = sys.argv[2] if len(sys.argv) > 2 else ""
    client = Client(sys.argv[1], key)
    pretty(client.request("GET", "/api/v1/health", auth=False))


if __name__ == "__main__":
    main()
