import { defineConfig } from '@playwright/test';

const baseURL = process.env.BASE_URL ?? 'http://localhost:8000';

export default defineConfig({
  testDir: 'tests',
  // The orchestrator runs one node per process and owns retries and
  // parallelism itself, so Playwright must not add its own.
  workers: 1,
  retries: 0,
  use: {
    baseURL,
    trace: 'retain-on-failure',
  },
  // No webServer here on purpose: nodes run in separate processes, so a server
  // owned by one of them would be killed after that node and take the session
  // with it. The system under test is started once, outside the run - as it
  // would be in CI. See README.md.
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
