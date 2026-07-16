import { test, expect, playdagMeta } from '@playdag/playwright';

// Signing in is done once, by this node. Everything downstream reuses the token
// it publishes rather than driving the login form again.
test('sign in', playdagMeta({
  id: 'auth.login',
  description: 'Sign in through the UI and publish the session token',
  provides: ['session.token'],
  criticality: 'BLOCKER',
  priority: 10,
  retries: 1,
  timeout: '90s',
  scope: 'suite',
  tags: ['chromium'],
}), async ({ page, playdag }) => {
  await page.goto('/login');
  await page.locator('#user').fill('demo');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('#status')).toHaveText('signed in');

  const token = await page.evaluate(() => localStorage.getItem('token'));
  expect(token).toBeTruthy();
  playdag.provide('session.token', token!);
});

test('create a card', playdagMeta({
  id: 'card.create',
  description: 'Create a card with the session the login node established',
  requires: ['session.token'],
  provides: ['card.id'],
  criticality: 'BLOCKER',
  priority: 5,
  retries: 0,
  timeout: '90s',
  scope: 'suite',
  tags: ['chromium'],
}), async ({ page, playdag }) => {
  await signedIn(page, playdag.get('session.token'));

  await page.goto('/board');
  await expect(page.locator('#whoami')).toHaveText('signed in as demo');
  await page.locator('#title').fill('Written by playdag');
  await page.getByRole('button', { name: 'New card' }).click();

  const card = page.getByTestId('card').first();
  await expect(card).toContainText('Written by playdag');
  playdag.provide('card.id', (await card.getAttribute('data-card-id'))!);
});

// Independent of the card work: it needs only the session, so the orchestrator
// is free to run it alongside the others. A suite of one long chain has nothing
// to parallelise; a real one looks like this.
test('view the board', playdagMeta({
  id: 'board.view',
  description: 'Open the board with the established session',
  requires: ['session.token'],
  criticality: 'BLOCKER',
  priority: 5,
  timeout: '90s',
  scope: 'suite',
  tags: ['chromium'],
}), async ({ page, playdag }) => {
  await signedIn(page, playdag.get('session.token'));

  await page.goto('/board');
  await expect(page.locator('#whoami')).toHaveText('signed in as demo');
  await expect(page.locator('#new')).toBeVisible();
});

test('count the cards', playdagMeta({
  id: 'board.count',
  description: 'Read the board through the API with the established session',
  requires: ['session.token'],
  criticality: 'NON_CRITICAL',
  priority: 3,
  timeout: '90s',
  scope: 'suite',
  tags: ['chromium'],
}), async ({ page, playdag }) => {
  const token = playdag.get('session.token');
  await signedIn(page, token);

  await page.goto('/board');
  const count = await page.evaluate(async (t) => {
    const r = await fetch('/api/cards', { headers: { authorization: 'Bearer ' + t } });
    return (await r.json()).cards.length;
  }, token);
  expect(count).toBeGreaterThanOrEqual(0);
});

test('move a card to done', playdagMeta({
  id: 'card.move',
  description: 'Move the created card to the done column',
  requires: ['session.token', 'card.id'],
  criticality: 'NON_CRITICAL',
  priority: 1,
  retries: 1,
  timeout: '90s',
  scope: 'suite',
  tags: ['chromium'],
}), async ({ page, playdag }) => {
  await signedIn(page, playdag.get('session.token'));
  const cardId = playdag.get('card.id');

  await page.goto('/board');
  const card = page.locator(`[data-card-id="${cardId}"]`);
  await expect(card).toHaveAttribute('data-column', 'todo');

  await card.getByRole('button', { name: 'Move to done' }).click();
  await expect(page.locator(`[data-card-id="${cardId}"]`)).toHaveAttribute('data-column', 'done');
});

/** Restores the session the login node established, without a second login. */
async function signedIn(page: import('@playwright/test').Page, token: string) {
  await page.addInitScript((t) => localStorage.setItem('token', t), token);
}
