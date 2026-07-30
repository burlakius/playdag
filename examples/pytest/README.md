# pytest example

The same three-node suite as the [Playwright](../playwright/README.md) and
[generic](../process/README.md) examples, this time written as pytest tests
against the [demo application](../demo-app/server.js)'s HTTP API:

```
auth.login ──session.token──► card.create ──card.id──► card.move
                    └────────────session.token───────────┘
```

`auth.login` is the only node that signs in. `card.create` and `card.move`
read the session out of the context instead, so signing in happens once per
run instead of once per test - the repeated work this orchestrator exists to
remove.

## Run

```bash
# from the repository root
python3 -m venv .venv && . .venv/bin/activate
pip install -e adapters/pytest_playdag -e examples/pytest
go build ./cmd/orchestrator/

# the system under test is started once, outside the run
node examples/demo-app/server.js 8000 &

# suite.json is generated from the markers on the tests
./orchestrator extract -extractor pytest \
  -basedir examples/pytest -o examples/pytest/suite.json

./orchestrator -executor pytest \
  -suite examples/pytest/suite.json \
  -basedir examples/pytest \
  -baseurl http://localhost:8000
```

Each node is a separate `python3 -m pytest <nodeid>` process; pytest node ids
are already unique, so - unlike Playwright - no `--grep` tag is needed to
select one.

## How a test declares itself

The `playdag` marker carries the node's contract, and the `playdag` fixture
gives the test its context:

```python
@pytest.mark.playdag(id="card.create", requires=["session.token"], provides=["card.id"], criticality="BLOCKER")
def test_create_card(playdag):
    token = playdag.get("session.token")   # provided by auth.login
    ...
    playdag.provide("card.id", card_id)    # consumed by card.move
```

`suite.json` is generated from exactly these markers by `orchestrator extract`,
so it is a build artefact and is not committed. A broken contract - an unknown
key, a key provided by two tests, a cycle - is an error at extraction time,
pointing at the file and line that declared it.
