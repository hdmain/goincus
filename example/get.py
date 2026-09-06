#!/usr/bin/env python3
"""Get one instance by id.

Usage:
  export GOINCUS_API_KEY=gic_...
  python get.py <instance-id>
"""

from __future__ import annotations

import sys

from common import pretty, request


def main() -> None:
    if len(sys.argv) < 2:
        print("usage: get.py <instance-id>", file=sys.stderr)
        sys.exit(2)
    pretty(request("GET", f"/api/v1/instances/{sys.argv[1]}"))


if __name__ == "__main__":
    main()
