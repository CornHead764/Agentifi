import { defineConfig, devices } from '@playwright/test'

const PORT = Number(process.env.DEMO_PORT ?? 5177)

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.ts',
  outputDir: 'output/results',
  fullyParallel: true,
  // More at once and the recordings stutter.
  workers: 2,
  reporter: [['list']],
  timeout: 300_000,
  use: {
    baseURL: `http://localhost:${PORT}`,
    serviceWorkers: 'block',
    colorScheme: 'light',
    locale: 'en-US',
    timezoneId: 'America/New_York',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    command: `npx vite --port ${PORT} --strictPort`,
    cwd: '..',
    url: `http://localhost:${PORT}`,
    reuseExistingServer: true,
    timeout: 60_000,
  },
})
