#!/usr/bin/env python3
"""Restart an instance.

Usage:
  python restart.py <url> <api-key> <instance-id>
"""

from __future__ import annotations

import sys

from common import parse_conn, pretty


def main() -> None:
    client, args = parse_conn(
        sys.argv[1:],
        "usage: restart.py <url> <api-key> <instance-id>",
    )
    if not args:
        print("usage: restart.py <url> <api-key> <instance-id>", file=sys.stderr)
        sys.exit(2)
    pretty(client.request("POST", f"/api/v1/instances/{args[0]}/restart"))


if __name__ == "__main__":
    main()
