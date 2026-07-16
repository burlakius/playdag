#!/usr/bin/env python3
"""Moves the card the create node produced, reusing the same session."""
import json
import os
import sys
import urllib.request

req = json.load(sys.stdin)
base = req.get("base_url") or os.environ.get("BASE_URL", "http://localhost:8000")

token = req["context"]["session.token"]
card_id = req["context"]["card.id"]

try:
    body = json.dumps({"column": "done"}).encode()
    r = urllib.request.Request(
        base + "/api/cards/" + card_id + "/move",
        data=body,
        headers={"content-type": "application/json", "authorization": "Bearer " + token},
    )
    with urllib.request.urlopen(r, timeout=10) as resp:
        column = json.load(resp)["column"]
except Exception as e:
    print(json.dumps({"schema_version": "1.0", "status": "FAILED", "error": str(e)}))
    sys.exit(0)

if column != "done":
    print(json.dumps({
        "schema_version": "1.0",
        "status": "FAILED",
        "error": "card is in column %r, want 'done'" % column,
    }))
    sys.exit(0)

print(json.dumps({"schema_version": "1.0", "status": "PASSED"}))
