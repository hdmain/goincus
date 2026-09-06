#!/usr/bin/env python3
"""Create a NAT VPS instance.

Usage:
  export GOINCUS_API_KEY=gic_...
  python create.py [name]

Example:
  python create.py web-1
"""

from __future__ import annotations

import sys
import time

from common import pretty, request


def main() -> None:
    name = sys.argv[1] if len(sys.argv) > 1 else f"vps-{int(time.time())}"
    body = {
        "name": name,
        "image": "ubuntu/24.04",
        "cpu_cores": 1,
        "memory_mb": 512,
        "storage_gb": 10,
        "internal_ports": [22, 80],
    }
    inst = request("POST", "/api/v1/instances", body)
    pretty(inst)
    print(f"\ncreated id={inst['id']} status={inst['status']}", file=sys.stderr)


if __name__ == "__main__":
    main()
