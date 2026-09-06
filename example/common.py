"""Shared helpers for goincus API examples.

Environment:
  GOINCUS_URL      Base URL (default: http://127.0.0.1:9603)
  GOINCUS_API_KEY  API key (required for authenticated endpoints)
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
from typing import Any


def base_url() -> str:
    return os.environ.get("GOINCUS_URL", "http://127.0.0.1:9603").rstrip("/")


def api_key() -> str:
    key = os.environ.get("GOINCUS_API_KEY", "").strip()
    if not key:
        print("error: set GOINCUS_API_KEY", file=sys.stderr)
        sys.exit(1)
    return key


def request(
    method: str,
    path: str,
    body: dict[str, Any] | None = None,
    *,
    auth: bool = True,
) -> Any:
    url = f"{base_url()}{path}"
    data = None
    headers = {"Accept": "application/json"}
    if auth:
        headers["Authorization"] = f"Bearer {api_key()}"
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


def pretty(obj: Any) -> None:
    print(json.dumps(obj, indent=2, sort_keys=True))
