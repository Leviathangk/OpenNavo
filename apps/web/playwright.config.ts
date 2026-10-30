import { defineConfig } from '@playwright/test';

// Web end-to-end tests (05 §10): use a backend populated with seed-e2e data (make e2e-up, default 127.0.0.1:18082).
// Alternate ports require matching backend CORS origins. Never reuse an existing service and risk connecting to the wrong backend.
const port = Number(process.env.E2E_WEB_PORT || 3000);
const baseURL = `http://localhost:${port}`;

export default defineConfig({
  testDir: 'e2e',
  fullyParallel: true,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL,
    channel: process.env.CI ? undefined : 'chrome',
    viewport: { width: 1440, height: 900 },
    trace: 'retain-on-failure'
  },
  webServer: {
    command: 'pnpm dev:e2e',
    url: baseURL,
    reuseExistingServer: false,
    timeout: 180_000
  }
});
