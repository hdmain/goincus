#!/usr/bin/env python3
"""Remove a proxy port mapping.

Usage:
  python remove_port.py <url> <api-key> <instance-id> <port-id>
"""

from __future__ import annotations

import sys

from common import parse_conn


def main() -> None:
    usage = "usage: remove_port.py <url> <api-key> <instance-id> <port-id>"
    client, args = parse_conn(sys.argv[1:], usage)
    if len(args) < 2:
        print(usage, file=sys.stderr)
        sys.exit(2)
    client.request("DELETE", f"/api/v1/instances/{args[0]}/ports/{args[1]}")
    print(f"removed port {args[1]} from {args[0]}")


if __name__ == "__main__":
    main()
