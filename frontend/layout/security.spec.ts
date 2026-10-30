import { expect, test, type Page, type Request } from '@playwright/test'

import { serveFixtures } from './api'
import { id } from './fixtures/core'
import { TODAY } from './fixtures/dates'

/**
 * The Sessions card in Security settings, driven by clicks: a static render
 * keeps no state between them, so the token shown once and the signed-out page
 * after "Sign out everywhere" can only be seen in a browser.
 */

const TOKEN = 'example-personal-token-value'

/** Security settings over the fixtures, with every write it sends recorded. */
async function openSecurity(page: Page): Promise<Request[]> {
  const missing: string[] = []
  const writes: Request[] = []
  await page.clock.setFixedTime(TODAY)
  await page.addInitScript(() => localStorage.setItem('agentifi.session', 'layout-fixture-session'))
  await serveFixtures(page, missing)
  page.on('request', (request) => {
    if (request.url().includes('/api/') && request.method() !== 'GET') writes.push(request)
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/settings/security')
  await page.waitForLoadState('networkidle')
  expect(missing).toEqual([])
  return writes
}

function sessionRow(page: Page, name: string) {
  return page.getByRole('listitem').filter({ hasText: name })
}

test('a new personal token is shown once, and gone once dismissed', async ({ page }) => {
  const writes = await openSecurity(page)
  // Registered after the fixtures, so it wins.
  await page.route('**/api/auth/personal-tokens', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        token: TOKEN,
        session: {
          id: id('sess', 4), kind: 'personal', name: 'Backup script', current: false,
          created_at: '2026-06-14T08:00:00Z', last_used_at: '2026-06-14T08:00:00Z',
          expires_at: '2027-06-14T08:00:00Z',
        },
      }),
    }),
  )

  await page.getByRole('button', { name: 'New personal token' }).click()
  const naming = page.getByRole('dialog', { name: 'New personal token' })
  await naming.getByLabel('Name').fill('Backup script')
  await naming.getByRole('button', { name: 'Create token' }).click()

  const shown = page.getByRole('dialog', { name: 'Copy this token now' })
  await expect(shown).toContainText(TOKEN)
  await expect(shown).toContainText('Shown only once. If lost, sign out Backup script')
  expect(writes.map((write) => [write.method(), new URL(write.url()).pathname])).toEqual([
    ['POST', '/api/auth/personal-tokens'],
  ])
  expect(writes[0].postDataJSON()).toEqual({ name: 'Backup script' })

  await shown.getByRole('button', { name: 'Done' }).click()
  await expect(shown).toBeHidden()
  await expect(page.getByText(TOKEN)).toHaveCount(0)
})

test('another session signs out behind a confirm, and this browser cannot be', async ({ page }) => {
  const writes = await openSecurity(page)

  await expect(sessionRow(page, 'Firefox on Linux')).toContainText('This browser')
  await expect(sessionRow(page, 'Firefox on Linux').getByRole('button', { name: 'Sign out' })).toHaveCount(0)

  await sessionRow(page, 'MCP server').getByRole('button', { name: 'Sign out' }).click()
  const confirm = page.getByRole('dialog', { name: 'Sign out MCP server?' })
  await expect(confirm).toBeVisible()
  expect(writes).toEqual([])

  await confirm.getByRole('button', { name: 'Sign out' }).click()
  await expect(confirm).toBeHidden()
  expect(writes.map((write) => [write.method(), new URL(write.url()).pathname])).toEqual([
    ['DELETE', `/api/auth/sessions/${id('sess', 3)}`],
  ])
})

test('sign out everywhere asks first, and then leaves this browser signed out', async ({ page }) => {
  const writes = await openSecurity(page)
  const everywhere = page.getByRole('button', { name: 'Sign out everywhere' })

  await everywhere.click()
  const confirm = page.getByRole('dialog', { name: 'Sign out everywhere?' })
  await expect(confirm).toContainText('this one included')
  await confirm.getByRole('button', { name: 'Cancel' }).click()
  await expect(confirm).toBeHidden()
  expect(writes).toEqual([])
  await expect(page.getByRole('heading', { name: 'Sessions' })).toBeVisible()

  await everywhere.click()
  await confirm.getByRole('button', { name: 'Sign out everywhere' }).click()
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible()
  expect(writes.map((write) => [write.method(), new URL(write.url()).pathname])).toEqual([
    ['POST', '/api/auth/sessions/revoke-all'],
  ])
  expect(await page.evaluate(() => localStorage.getItem('agentifi.session'))).toBeNull()
})
