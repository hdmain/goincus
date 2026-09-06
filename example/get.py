#!/usr/bin/env python3
"""Get one instance by id.

Usage:
  python get.py <url> <api-key> <instance-id>
"""

from __future__ import annotations

import sys

from common import parse_conn, pretty


def main() -> None:
    client, args = parse_conn(
        sys.argv[1:],
        "usage: get.py <url> <api-key> <instance-id>",
    )
    if not args:
        print("usage: get.py <url> <api-key> <instance-id>", file=sys.stderr)
        sys.exit(2)
    pretty(client.request("GET", f"/api/v1/instances/{args[0]}"))


if __name__ == "__main__":
    main()
