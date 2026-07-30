"""A three-node suite driving the demo application over its HTTP API.

auth.login publishes session.token; card.create and card.move reuse it instead
of signing in again, which is the repeated work playdag exists to remove.
"""
import json
import os
import urllib.request

import pytest


def base_url() -> str:
    return os.environ.get("BASE_URL", "http://localhost:8000")


@pytest.mark.playdag(
    id="auth.login",
    description="Log in over the API and publish the session token",
    provides=["session.token"],
    criticality="BLOCKER",
    priority=10,
    retries=1,
    timeout="30s",
    scope="suite",
)
def test_login(playdag):
    body = json.dumps({"user": "demo"}).encode()
    req = urllib.request.Request(
        base_url() + "/api/login", data=body, headers={"content-type": "application/json"}
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        token = json.load(resp)["token"]
    playdag.provide("session.token", token)


@pytest.mark.playdag(
    id="card.create",
    description="Create a card with the session the login node established",
    requires=["session.token"],
    provides=["card.id"],
    criticality="BLOCKER",
    priority=5,
    timeout="30s",
    scope="suite",
)
def test_create_card(playdag):
    # No second login: the token comes from the node that already logged in.
    token = playdag.get("session.token")
    body = json.dumps({"title": "Written by playdag"}).encode()
    req = urllib.request.Request(
        base_url() + "/api/cards",
        data=body,
        headers={"content-type": "application/json", "authorization": "Bearer " + token},
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        card_id = json.load(resp)["id"]
    playdag.provide("card.id", card_id)


@pytest.mark.playdag(
    id="card.move",
    description="Move the created card to the done column",
    requires=["session.token", "card.id"],
    criticality="NON_CRITICAL",
    priority=1,
    retries=1,
    timeout="30s",
    scope="suite",
)
def test_move_card(playdag):
    token = playdag.get("session.token")
    card_id = playdag.get("card.id")
    body = json.dumps({"column": "done"}).encode()
    req = urllib.request.Request(
        base_url() + "/api/cards/" + card_id + "/move",
        data=body,
        headers={"content-type": "application/json", "authorization": "Bearer " + token},
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        column = json.load(resp)["column"]
    assert column == "done"
