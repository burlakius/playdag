# Generic example: agents without a test framework

The same orchestrator, the same DAG, and the same context flow as the
[Playwright example](../playwright/README.md) — but the agents here are plain
Python scripts calling an HTTP API. No browser and no test framework is
involved, which is the point: the core knows only the
[protocol](../../docs/PROTOCOL.md).

```
auth.login ──session.token──► card.create ──card.id──► card.move
                    └────────────session.token───────────┘
```

`auth.login` logs in once and publishes `session.token`. The other nodes read it
from the context instead of logging in again, which is the repeated work the
orchestrator is there to remove.

## Run

Start the demo application, generate the suite from the agents, then run it:

```bash
node ../demo-app/server.js 8000 &

cd ../..
go build ./cmd/orchestrator/

# suite.json is generated, never written by hand
./orchestrator extract -extractor process \
  -basedir examples/process -o examples/process/suite.json

./orchestrator -executor process \
  -suite examples/process/suite.json \
  -basedir examples/process \
  -baseurl http://localhost:8000
```

Every node should pass, with `session.token` and `card.id` in the context
snapshot.

## Writing an agent

An agent declares what it needs and produces in its header, and the extractor
builds the graph from those declarations:

```python
"""Creates a card with the session the login node already established.

playdag:id: card.create
playdag:requires: session.token
playdag:provides: card.id
playdag:criticality: BLOCKER
playdag:timeout: 30s
"""
```

It then reads the request on stdin and writes one JSON response on stdout:

```python
req = json.load(sys.stdin)
token = req["context"]["session.token"]
...
print(json.dumps({"schema_version": "1.0", "status": "PASSED",
                  "provides": {"card.id": card_id}}))
```

Scripts ending in `.py` are run with `python3`; anything else executable is run
directly, so an agent may be written in any language that can read JSON.
