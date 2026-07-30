# @playdag/playwright

Playwright adapter for the [playdag](../../README.md) orchestrator. It is a
consumer of the [agent protocol](../../docs/PROTOCOL.md), not part of the
orchestrator: the Go core never imports it and never learns what Playwright is.

It provides two pieces:

- a **fixture** (`playdag`) that injects the context an earlier node produced and
  publishes the values this node promises;
- a **reporter** that reports the node's outcome - status, provides, artifacts -
  back to the orchestrator in the protocol format.

## Install

```bash
npm install --save-dev @playdag/playwright
```

## Use

Import `test` from the adapter instead of `@playwright/test`, and declare the
node's contract with `playdagMeta`:

```ts
import { test, expect, playdagMeta } from '@playdag/playwright';

test('log in', playdagMeta({
  id: 'auth.login',
  provides: ['session.token'],
  criticality: 'BLOCKER',
  priority: 10,
}), async ({ page, playdag }) => {
  await page.goto('/login');
  await page.getByLabel('User').fill('demo');
  await page.getByRole('button', { name: 'Sign in' }).click();

  const token = await page.evaluate(() => localStorage.getItem('token'));
  playdag.provide('session.token', token!);
});

test('create a card', playdagMeta({
  id: 'card.create',
  requires: ['session.token'],
  provides: ['card.id'],
  criticality: 'BLOCKER',
}), async ({ page, playdag }) => {
  // No second login: the token comes from the node that already logged in.
  const token = playdag.get('session.token');
  await page.addInitScript((t) => localStorage.setItem('token', t), token);

  await page.goto('/board');
  await page.getByRole('button', { name: 'New card' }).click();
  playdag.provide('card.id', await page.getByTestId('card-id').innerText());
});
```

`playdagMeta` returns the `@id:<node>` tag the orchestrator greps for plus the
annotations describing the contract, so the node's metadata lives in the test
rather than in a hand-written suite file.

Run the suite through the orchestrator:

```bash
orchestrator -executor playwright -suite suite.json -basedir .
```

Each node is one `npx playwright test --grep "@id:<node>"` process. The
orchestrator passes the context in `PLAYDAG_CONTEXT` and reads the reporter's
response from the file named by `PLAYDAG_RESULT`; both are set for you.

The fixture also works without the orchestrator, so `npx playwright test` still
runs and debugs a spec directly - `playdag.get` simply finds no context there.
