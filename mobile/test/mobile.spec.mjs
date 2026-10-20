import { expect, test } from '../../frontend/node_modules/@playwright/test/index.mjs'
import { serveAssets } from './assets.mjs'

const address = 'http://192.0.2.1:8100'

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 640 })
  await serveAssets(page, '../dist/')
  await page.route('http://192.0.2.1:8100/api/**', (route) => {
    const path = new URL(route.request().url()).pathname
    return route.fulfill({ json: path.endsWith('/first-account') ? { open: false } : { enabled: false } })
  })
})

test('first run waits for a server, then boots the shared login with the runtime API base', async ({ page }) => {
  const errors = []
  page.on('pageerror', (error) => errors.push(error.message))
  const requests = []
  page.on('request', (request) => { if (request.url().includes('/api/')) requests.push(request.url()) })
  await page.goto('/')
  await expect(page.getByRole('dialog')).toBeVisible()
  expect(requests).toEqual([])
  await page.getByLabel('Server URL').fill(address + '/api/')
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(page.getByRole('dialog')).not.toBeVisible()
  await expect(page.getByLabel('Password', { exact: true })).toBeVisible()
  expect(await page.evaluate(() => window.agentifiApiBase)).toBe(address + '/api')
  expect(requests.every((url) => url.startsWith(address))).toBe(true)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.reload()
  await expect(page.getByLabel('Password', { exact: true })).toBeVisible()
  expect(errors).toEqual([])
})

test('bad server stays in setup, and native Server action opens editable settings', async ({ page }) => {
  await page.route('http://192.0.2.2/api/**', (route) => route.fulfill({ body: '<html>Wrong service</html>', contentType: 'text/html' }))
  await page.goto('/')
  await page.getByLabel('Server URL').fill('http://192.0.2.2')
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('did not answer as an Agentifi')
  expect(await page.evaluate(() => localStorage.getItem('agentifi.mobile.server'))).toBeNull()
  await page.getByLabel('Server URL').fill(address)
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(page.getByRole('dialog')).not.toBeVisible()
  await page.evaluate(() => window.dispatchEvent(new Event('agentifi:server-settings')))
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(page.getByLabel('Server URL')).toHaveValue(address)
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('dialog')).not.toBeVisible()
})

test('changing servers reloads with no previous token or space', async ({ page }) => {
  const next = 'http://192.0.2.2'
  const headers = []
  await page.route(`${next}/api/**`, (route) => {
    headers.push(route.request().headers())
    const path = new URL(route.request().url()).pathname
    return route.fulfill({ json: path.endsWith('/first-account') ? { open: false } : { enabled: false } })
  })
  await page.goto('/')
  await page.getByLabel('Server URL').fill(address)
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(page.getByLabel('Password', { exact: true })).toBeVisible()
  await page.evaluate(() => {
    localStorage.setItem('agentifi.accessToken', 'invented-old-server-token')
    localStorage.setItem('agentifi.activeSpace.user-a', 'space-a')
    window.dispatchEvent(new Event('agentifi:server-settings'))
  })
  await page.getByLabel('Server URL').fill(next)
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect.poll(() => page.evaluate(() => window.agentifiApiBase)).toBe(next + '/api')
  await expect(page.getByLabel('Password', { exact: true })).toBeVisible()
  expect(await page.evaluate(() => localStorage.getItem('agentifi.accessToken'))).toBeNull()
  expect(await page.evaluate(() => localStorage.getItem('agentifi.activeSpace.user-a'))).toBeNull()
  expect(headers.length).toBeGreaterThan(1)
  expect(headers.every((h) => !h.authorization && !h['x-space-id'])).toBe(true)
})
