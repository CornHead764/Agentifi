import { defineConfig } from '../../frontend/node_modules/@playwright/test/index.mjs'

export default defineConfig({
  testDir: '.',
  testMatch: '*.spec.mjs',
  outputDir: 'output',
  workers: 4,
  timeout: 60_000,
  use: {
    baseURL: 'http://agentifi.test',
    serviceWorkers: 'block',
    reducedMotion: 'reduce',
    colorScheme: 'light',
    locale: 'en-US',
    timezoneId: 'America/New_York',
  },
})
