#!/usr/bin/env python3
"""Restart an instance.

Usage:
  export GOINCUS_API_KEY=gic_...
  python restart.py <instance-id>
"""

from __future__ import annotations

import sys

from common import pretty, request


def main() -> None:
    if len(sys.argv) < 2:
        print("usage: restart.py <instance-id>", file=sys.stderr)
        sys.exit(2)
    pretty(request("POST", f"/api/v1/instances/{sys.argv[1]}/restart"))


if __name__ == "__main__":
    main()
