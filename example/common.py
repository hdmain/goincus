"""Shared helpers for goincus API examples.

Usage pattern for scripts:
  python <script>.py <url> <api-key> [args...]

Example:
  python create.py http://127.0.0.1:9603 gic_xxx web-1
"""

from __future__ import annotations

import json
import sys
import urllib.error
import urllib.request
from typing import Any


class Client:
    def __init__(self, base_url: str, api_key: str) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key

    def request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | None = None,
        *,
        auth: bool = True,
    ) -> Any:
        url = f"{self.base_url}{path}"
        data = None
        headers = {"Accept": "application/json"}
        if auth:
            headers["Authorization"] = f"Bearer {self.api_key}"
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"

        req = urllib.request.Request(url, data=data, headers=headers, method=method.upper())
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                raw = resp.read()
                if not raw:
                    return None
                return json.loads(raw.decode("utf-8"))
        except urllib.error.HTTPError as e:
            detail = e.read().decode("utf-8", errors="replace")
            print(f"HTTP {e.code} {method} {path}: {detail}", file=sys.stderr)
            sys.exit(1)
        except urllib.error.URLError as e:
            print(f"request failed: {e}", file=sys.stderr)
            sys.exit(1)


def parse_conn(argv: list[str], usage: str) -> tuple[Client, list[str]]:
    """Parse `<url> <api-key> ...` from argv (script name already removed)."""
    if len(argv) < 2:
        print(usage, file=sys.stderr)
        sys.exit(2)
    return Client(argv[0], argv[1]), argv[2:]


def pretty(obj: Any) -> None:
    print(json.dumps(obj, indent=2, sort_keys=True))
