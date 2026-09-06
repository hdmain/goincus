#!/usr/bin/env python3
"""Create a NAT VPS instance.

Usage:
  python create.py <url> <api-key> [name]

Example:
  python create.py http://127.0.0.1:9603 gic_xxx web-1
"""

from __future__ import annotations

import sys
import time

from common import parse_conn, pretty


def main() -> None:
    client, args = parse_conn(
        sys.argv[1:],
        "usage: create.py <url> <api-key> [name]",
    )
    name = args[0] if args else f"vps-{int(time.time())}"
    body = {
        "name": name,
        "image": "ubuntu/24.04",
        "cpu_cores": 1,
        "memory_mb": 512,
        "storage_gb": 10,
        "internal_ports": [22, 80],
    }
    inst = client.request("POST", "/api/v1/instances", body)
    pretty(inst)
    print(f"\ncreated id={inst['id']} status={inst['status']}", file=sys.stderr)


if __name__ == "__main__":
    main()
