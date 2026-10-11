import { defineConfig, devices } from '@playwright/test'

// Browser tests against a running Mediarium with a fresh, empty config
// folder (the first test goes through the setup wizard). Point them at it
// with MEDIARIUM_URL; see e2e/README.md.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: process.env.MEDIARIUM_URL ?? 'http://localhost:8264',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    // Locally an installed Edge or Chrome saves downloading a browser.
    channel: process.env.PW_CHANNEL || undefined,
  },
  projects: [
    { name: 'setup', testMatch: /setup\.spec\.ts/, use: { ...devices['Desktop Chrome'], channel: process.env.PW_CHANNEL || undefined } },
    {
      name: 'app',
      testIgnore: /setup\.spec\.ts/,
      dependencies: ['setup'],
      use: { ...devices['Desktop Chrome'], channel: process.env.PW_CHANNEL || undefined, storageState: 'e2e/.auth/admin.json' },
    },
  ],
})
