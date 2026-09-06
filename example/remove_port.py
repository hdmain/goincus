#!/usr/bin/env python3
"""Remove a proxy port mapping.

Usage:
  export GOINCUS_API_KEY=gic_...
  python remove_port.py <instance-id> <port-id>
"""

from __future__ import annotations

import sys

from common import request


def main() -> None:
    if len(sys.argv) < 3:
        print("usage: remove_port.py <instance-id> <port-id>", file=sys.stderr)
        sys.exit(2)
    request("DELETE", f"/api/v1/instances/{sys.argv[1]}/ports/{sys.argv[2]}")
    print(f"removed port {sys.argv[2]} from {sys.argv[1]}")


if __name__ == "__main__":
    main()
