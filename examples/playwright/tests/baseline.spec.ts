import { test, expect, Page } from '@playwright/test';

/**
 * The baseline: the same three scenarios written the way Playwright is normally
 * used — every test independent and responsible for its own state, so every test
 * signs in again, and the card test creates a card the move test then creates a
 * second time.
 *
 * This is the prototype the orchestrated suite in board.spec.ts is measured
 * against; experiments/run.sh runs it with plain `npx playwright test`. It
 * declares no playdag metadata, so the extractor ignores it and it is never part
 * of an orchestrated run.
 */

async function signIn(page: Page): Promise<string> {
  await page.goto('/login');
  await page.locator('#user').fill('demo');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('#status')).toHaveText('signed in');

  const token = await page.evaluate(() => localStorage.getItem('token'));
  expect(token).toBeTruthy();
  return token!;
}

async function createCard(page: Page): Promise<string> {
  await page.goto('/board');
  await expect(page.locator('#whoami')).toHaveText('signed in as demo');
  await page.locator('#title').fill('Written by playdag');
  await page.getByRole('button', { name: 'New card' }).click();

  const card = page.getByTestId('card').last();
  await expect(card).toContainText('Written by playdag');
  return (await card.getAttribute('data-card-id'))!;
}

test('baseline: sign in', async ({ page }) => {
  await signIn(page);
});

test('baseline: create a card', async ({ page }) => {
  await signIn(page);
  await createCard(page);
});

test('baseline: view the board', async ({ page }) => {
  await signIn(page);
  await page.goto('/board');
  await expect(page.locator('#whoami')).toHaveText('signed in as demo');
  await expect(page.locator('#new')).toBeVisible();
});

test('baseline: count the cards', async ({ page }) => {
  const token = await signIn(page);
  await page.goto('/board');
  const count = await page.evaluate(async (t) => {
    const r = await fetch('/api/cards', { headers: { authorization: 'Bearer ' + t } });
    return (await r.json()).cards.length;
  }, token);
  expect(count).toBeGreaterThanOrEqual(0);
});

test('baseline: move a card to done', async ({ page }) => {
  await signIn(page);
  const cardId = await createCard(page);

  const card = page.locator(`[data-card-id="${cardId}"]`);
  await expect(card).toHaveAttribute('data-column', 'todo');
  await card.getByRole('button', { name: 'Move to done' }).click();
  await expect(page.locator(`[data-card-id="${cardId}"]`)).toHaveAttribute('data-column', 'done');
});
