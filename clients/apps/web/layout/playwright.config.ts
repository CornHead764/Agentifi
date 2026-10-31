import { defineConfig, devices } from '@playwright/test'

const PORT = Number(process.env.LAYOUT_PORT ?? 5176)

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  outputDir: 'output/results',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['list'], ['github']] : [['list']],
  timeout: 60_000,
  use: {
    baseURL: `http://localhost:${PORT}`,
    // The service worker would answer `/api` before the fixtures could.
    serviceWorkers: 'block',
    reducedMotion: 'reduce',
    colorScheme: 'light',
    locale: 'en-US',
    timezoneId: 'America/New_York',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: `npx vite --port ${PORT} --strictPort`,
    cwd: '..',
    url: `http://localhost:${PORT}`,
    reuseExistingServer: false,
    timeout: 60_000,
  },
})
