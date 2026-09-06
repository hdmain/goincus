#!/usr/bin/env python3
"""List all instances.

Usage:
  python list.py <url> <api-key>
"""

from __future__ import annotations

import sys

from common import parse_conn, pretty


def main() -> None:
    client, _ = parse_conn(sys.argv[1:], "usage: list.py <url> <api-key>")
    pretty(client.request("GET", "/api/v1/instances"))


if __name__ == "__main__":
    main()
