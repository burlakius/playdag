#!/usr/bin/env python3
"""Logs in and hands the session token to every node that follows."""
import json
import os
import sys
import urllib.request

req = json.load(sys.stdin)
base = req.get("base_url") or os.environ.get("BASE_URL", "http://localhost:8000")

try:
    body = json.dumps({"user": "demo"}).encode()
    r = urllib.request.Request(
        base + "/api/login", data=body, headers={"content-type": "application/json"}
    )
    with urllib.request.urlopen(r, timeout=10) as resp:
        token = json.load(resp)["token"]
except Exception as e:
    print(json.dumps({"schema_version": "1.0", "status": "FAILED", "error": str(e)}))
    sys.exit(0)

print(json.dumps({
    "schema_version": "1.0",
    "status": "PASSED",
    "provides": {"session.token": token},
}))
