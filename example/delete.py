#!/usr/bin/env python3
"""Delete an instance.

Usage:
  python delete.py <url> <api-key> <instance-id>
"""

from __future__ import annotations

import sys

from common import parse_conn


def main() -> None:
    client, args = parse_conn(
        sys.argv[1:],
        "usage: delete.py <url> <api-key> <instance-id>",
    )
    if not args:
        print("usage: delete.py <url> <api-key> <instance-id>", file=sys.stderr)
        sys.exit(2)
    client.request("DELETE", f"/api/v1/instances/{args[0]}")
    print(f"deleted {args[0]}")


if __name__ == "__main__":
    main()
