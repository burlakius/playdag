# Playwright example

A three-node suite driving a real browser against the
[demo application](../demo-app/server.js):

```
auth.login ──session.token──► card.create ──card.id──► card.move
                    └────────────session.token───────────┘
```

`auth.login` is the only node that drives the login form. `card.create` and
`card.move` restore the session from the context, so signing in happens once per
run instead of once per test — the repeated work this orchestrator exists to
remove.

The [generic example](../process/README.md) runs the same graph with Python
agents and no browser, on the same core.

## Run

```bash
# from the repository root
npm install
npx playwright install chromium
go build ./cmd/orchestrator/

# the system under test is started once, outside the run
node examples/demo-app/server.js 8000 &

# suite.json is generated from the annotations in the tests
./orchestrator extract -extractor playwright \
  -basedir examples/playwright -o examples/playwright/suite.json

./orchestrator -executor playwright \
  -suite examples/playwright/suite.json \
  -basedir examples/playwright \
  -baseurl http://localhost:8000
```

Each node is a separate `npx playwright test --grep "@id:<node>"` process. That
is also why the application is started separately rather than through
Playwright's `webServer`: a server owned by one node's process would be killed
when that node finished, taking the session with it.

## How a test declares itself

`playdagMeta` produces the `@id:` tag the orchestrator greps for and the
annotations describing the node's data contract, so the metadata lives next to
the test:

```ts
test('create a card', playdagMeta({
  id: 'card.create',
  requires: ['session.token'],
  provides: ['card.id'],
  criticality: 'BLOCKER',
}), async ({ page, playdag }) => {
  const token = playdag.get('session.token');   // provided by auth.login
  ...
  playdag.provide('card.id', id);               // consumed by card.move
});
```

`suite.json` is generated from exactly these annotations by
`orchestrator extract`, so it is a build artefact and is not committed. Nothing
describes a node twice, and a broken contract — an unknown key, a key provided
by two tests, a cycle — is an error at extraction time, pointing at the file and
line that declared it.

## Artifacts

`trace: 'retain-on-failure'` is on, and the reporter passes whatever Playwright
produced back to the orchestrator, so a failed node lists its trace in the
report:

```
✗ FAILED  card.create          expect(received).toHaveText(expected)
           trace    test-results/board-create-a-card-chromium/trace.zip
```

Open one with `npx playwright show-trace <path>`.
