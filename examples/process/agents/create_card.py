#!/usr/bin/env python3
"""Creates a card with the session the login node already established.

playdag:id: card.create
playdag:description: Create a card with the established session
playdag:requires: session.token
playdag:provides: card.id
playdag:criticality: BLOCKER
playdag:priority: 5
playdag:retries: 0
playdag:timeout: 30s
playdag:scope: suite
"""
import json
import os
import sys
import urllib.request

req = json.load(sys.stdin)
base = req.get("base_url") or os.environ.get("BASE_URL", "http://localhost:8000")

# No second login: the token comes from the node that logged in.
token = req["context"]["session.token"]

try:
    body = json.dumps({"title": "Written by playdag"}).encode()
    r = urllib.request.Request(
        base + "/api/cards",
        data=body,
        headers={"content-type": "application/json", "authorization": "Bearer " + token},
    )
    with urllib.request.urlopen(r, timeout=10) as resp:
        card_id = json.load(resp)["id"]
except Exception as e:
    print(json.dumps({"schema_version": "1.0", "status": "FAILED", "error": str(e)}))
    sys.exit(0)

print(json.dumps({
    "schema_version": "1.0",
    "status": "PASSED",
    "provides": {"card.id": card_id},
}))
