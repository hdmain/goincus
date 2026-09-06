#!/usr/bin/env python3
"""Start an instance.

Usage:
  python start.py <url> <api-key> <instance-id>
"""

from __future__ import annotations

import sys

from common import parse_conn, pretty


def main() -> None:
    client, args = parse_conn(
        sys.argv[1:],
        "usage: start.py <url> <api-key> <instance-id>",
    )
    if not args:
        print("usage: start.py <url> <api-key> <instance-id>", file=sys.stderr)
        sys.exit(2)
    pretty(client.request("POST", f"/api/v1/instances/{args[0]}/start"))


if __name__ == "__main__":
    main()
