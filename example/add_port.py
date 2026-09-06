#!/usr/bin/env python3
"""Add a proxy port mapping to an instance.

Usage:
  python add_port.py <url> <api-key> <instance-id> <internal-port> [protocol] [host-port]

Examples:
  python add_port.py http://127.0.0.1:9603 gic_xxx <id> 443
  python add_port.py http://127.0.0.1:9603 gic_xxx <id> 8080 tcp 21080
"""

from __future__ import annotations

import sys

from common import parse_conn, pretty


def main() -> None:
    usage = "usage: add_port.py <url> <api-key> <instance-id> <internal-port> [protocol] [host-port]"
    client, args = parse_conn(sys.argv[1:], usage)
    if len(args) < 2:
        print(usage, file=sys.stderr)
        sys.exit(2)

    body: dict = {
        "internal_port": int(args[1]),
        "protocol": args[2] if len(args) > 2 else "tcp",
    }
    if len(args) > 3:
        body["host_port"] = int(args[3])

    pretty(client.request("POST", f"/api/v1/instances/{args[0]}/ports", body))


if __name__ == "__main__":
    main()
