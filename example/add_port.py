#!/usr/bin/env python3
"""Add a proxy port mapping to an instance.

Usage:
  export GOINCUS_API_KEY=gic_...
  python add_port.py <instance-id> <internal-port> [protocol] [host-port]

Examples:
  python add_port.py <id> 443
  python add_port.py <id> 8080 tcp 21080
"""

from __future__ import annotations

import sys

from common import pretty, request


def main() -> None:
    if len(sys.argv) < 3:
        print(
            "usage: add_port.py <instance-id> <internal-port> [protocol] [host-port]",
            file=sys.stderr,
        )
        sys.exit(2)

    body: dict = {
        "internal_port": int(sys.argv[2]),
        "protocol": sys.argv[3] if len(sys.argv) > 3 else "tcp",
    }
    if len(sys.argv) > 4:
        body["host_port"] = int(sys.argv[4])

    pretty(request("POST", f"/api/v1/instances/{sys.argv[1]}/ports", body))


if __name__ == "__main__":
    main()
