#!/usr/bin/env python3
"""Fetch hourly CPU/RAM/disk/bandwidth metrics.

Usage:
  python metrics.py <url> <api-key> [name-or-id] [hours]

Examples:
  python metrics.py http://127.0.0.1:9603 gic_xxx
  python metrics.py http://127.0.0.1:9603 gic_xxx web-1 48
"""

from __future__ import annotations

import sys

from common import parse_conn, pretty


def main() -> None:
    client, args = parse_conn(
        sys.argv[1:],
        "usage: metrics.py <url> <api-key> [name-or-id] [hours]",
    )
    hours = "24"
    name = ""
    if args:
        # If only one arg and it looks like hours, treat as hours for all-instances.
        if len(args) == 1 and args[0].isdigit():
            hours = args[0]
        else:
            name = args[0]
            if len(args) > 1:
                hours = args[1]
    if name:
        path = f"/api/v1/instances/{name}/metrics?hours={hours}"
    else:
        path = f"/api/v1/metrics?hours={hours}"
    pretty(client.request("GET", path))


if __name__ == "__main__":
    main()
