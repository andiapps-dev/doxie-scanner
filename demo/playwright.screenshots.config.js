import { defineConfig } from '@playwright/test';

// Config for screenshots.spec.js (the README's still images), kept apart from
// the demo recording's: no video, and two device shapes instead of one.
export default defineConfig({
  testDir: '.',
  testMatch: 'screenshots.spec.js',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: process.env.DOXIE_BASE_URL || 'http://localhost:8080',
    colorScheme: 'light',
    video: 'off',
    trace: 'off',
    actionTimeout: 10_000,
  },
  projects: [
    { name: 'desktop', use: { viewport: { width: 1280, height: 860 }, deviceScaleFactor: 1 } },
    {
      name: 'phone',
      use: { viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true },
    },
  ],
});
