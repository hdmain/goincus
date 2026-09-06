#!/usr/bin/env python3
"""Delete an instance.

Usage:
  export GOINCUS_API_KEY=gic_...
  python delete.py <instance-id>
"""

from __future__ import annotations

import sys

from common import request


def main() -> None:
    if len(sys.argv) < 2:
        print("usage: delete.py <instance-id>", file=sys.stderr)
        sys.exit(2)
    request("DELETE", f"/api/v1/instances/{sys.argv[1]}")
    print(f"deleted {sys.argv[1]}")


if __name__ == "__main__":
    main()
