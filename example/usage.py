#!/usr/bin/env python3
"""Fetch daily disk + bandwidth usage for charts.

Usage:
  python usage.py <url> <api-key> <name-or-id> [days]

Example:
  python usage.py http://127.0.0.1:9603 gic_xxx web-1 30
"""

from __future__ import annotations

import sys

from common import parse_conn, pretty


def main() -> None:
    client, args = parse_conn(
        sys.argv[1:],
        "usage: usage.py <url> <api-key> <name-or-id> [days]",
    )
    if not args:
        print("name-or-id required", file=sys.stderr)
        sys.exit(2)
    name = args[0]
    days = args[1] if len(args) > 1 else "30"
    path = f"/api/v1/instances/{name}/usage?days={days}"
    pretty(client.request("GET", path))


if __name__ == "__main__":
    main()
