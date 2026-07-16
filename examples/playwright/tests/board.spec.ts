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
