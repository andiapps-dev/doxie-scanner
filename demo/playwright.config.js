import { defineConfig, devices } from '@playwright/test';

// Doubles as an integration test (real assertions against a running
// doxie-scanner container) and a demo recording: every run gets a video,
// not just failures, since record-demo.sh converts that video to
// demo.gif for the README.
export default defineConfig({
  testDir: '.',
  testMatch: 'demo.spec.js',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: 'list',
  use: {
    baseURL: process.env.DOXIE_BASE_URL || 'http://localhost:8080',
    viewport: { width: 1280, height: 860 },
    colorScheme: 'light',
    video: {
      mode: 'on',
      size: { width: 1280, height: 860 },
    },
    trace: 'off',
    actionTimeout: 10_000,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
