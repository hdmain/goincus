#!/usr/bin/env python3
"""Start an instance.

Usage:
  export GOINCUS_API_KEY=gic_...
  python start.py <instance-id>
"""

from __future__ import annotations

import sys

from common import pretty, request


def main() -> None:
    if len(sys.argv) < 2:
        print("usage: start.py <instance-id>", file=sys.stderr)
        sys.exit(2)
    pretty(request("POST", f"/api/v1/instances/{sys.argv[1]}/start"))


if __name__ == "__main__":
    main()
