# playdag-pytest

pytest adapter for the [playdag](../../README.md) orchestrator. It is a
consumer of the [agent protocol](../../docs/PROTOCOL.md), not part of the
orchestrator: the Go core never imports it and never learns that pytest exists.

It provides two pieces:

- a **fixture** (`playdag`) that injects the context an earlier node produced
  and publishes the values this node promises;
- a **marker** (`pytest.mark.playdag`) that declares the node's contract and
  is read by the metadata extractor, plus a **report hook** that returns the
  node's outcome - status, provides - to the orchestrator in protocol format.

## Install

```bash
pip install -e adapters/pytest_playdag
```

Installing the package registers the plugin as a `pytest11` entry point, so
`pytest.mark.playdag` is recognised without any extra configuration.

## Use

Declare the node's contract on the marker, and read or publish context through
the `playdag` fixture:

```python
import urllib.request
import pytest


@pytest.mark.playdag(id="auth.login", provides=["session.token"], criticality="BLOCKER", priority=10)
def test_login(playdag):
    resp = urllib.request.urlopen(f"{base_url}/api/login", data=b'{"user":"demo"}')
    token = json.load(resp)["token"]
    playdag.provide("session.token", token)


@pytest.mark.playdag(id="card.create", requires=["session.token"], provides=["card.id"], criticality="BLOCKER")
def test_create_card(playdag):
    # No second login: the token comes from the node that already logged in.
    token = playdag.get("session.token")
    ...
    playdag.provide("card.id", card_id)
```

Run the suite through the orchestrator:

```bash
orchestrator -executor pytest -suite suite.json -basedir .
```

Unlike the Playwright adapter, node selection needs no tag or `--grep`: pytest
node ids (`tests/test_login.py::test_login`) are already unique, so the
extractor records the node id directly and the executor selects tests by it.
The orchestrator passes the context in `PLAYDAG_CONTEXT` and reads the result
from the file named by `PLAYDAG_RESULT`; both are set for you.

The fixture also works without the orchestrator, so `pytest` still runs and
debugs a test directly - `playdag.get` simply finds no context there.

## Generating the suite

```bash
orchestrator extract -extractor pytest -basedir . -o suite.json
```

This runs `pytest --collect-only` with the plugin's own `--playdag-collect`
option, which writes every marked test's id, file, line, and contract to a
JSON manifest - the Python mirror of `playwright test --list --reporter=json`.
