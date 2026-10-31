import { expect, test, type Page } from '@playwright/test'

import { serveFixtures } from './api'
import { measureLayout } from './checks'
import { TODAY } from './fixtures/dates'

const SCREENSHOTS = 'layout/output/screenshots'

/** A server with no accounts yet; registered after the fixtures, so it wins. */
async function noAccountsYet(page: Page) {
  await page.route('**/api/auth/first-account', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"open":true}' }),
  )
}

async function open(page: Page, path: string, width: number, height: number, firstRun = false) {
  const missing: string[] = []
  await page.clock.setFixedTime(TODAY)
  if (!firstRun) {
    await page.addInitScript(() => localStorage.setItem('agentifi.accessToken', 'layout-fixture-token'))
  }
  await serveFixtures(page, missing)
  if (firstRun) await noAccountsYet(page)
  await page.setViewportSize({ width, height })
  await page.goto(path)
  await page.waitForLoadState('networkidle')
  expect(missing).toEqual([])
}

for (const [width, height] of [
  [390, 844],
  [1440, 900],
] as const) {
  test(`the first-account form fits at ${width}px`, async ({ page }) => {
    await open(page, '/login', width, height, true)
    await expect(page.getByRole('button', { name: 'Create account' })).toBeVisible()
    await expect(page.getByText('Bringing history from Quicken Simplifi?')).toBeVisible()
    expect(await page.evaluate(measureLayout)).toEqual([])
    await page.screenshot({ path: `${SCREENSHOTS}/login-first-account-${width}.png`, fullPage: true })
  })

  test(`the delete-space dialog fits at ${width}px`, async ({ page }) => {
    await open(page, '/settings/spaces', width, height)
    await page.getByRole('button', { name: 'Actions for Sample Household' }).click()
    await page.getByRole('menuitem', { name: 'Delete space…' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('button', { name: 'Delete space' })).toBeDisabled()
    await dialog.getByLabel('Type Sample Household to confirm').fill('Sample Household')
    await expect(dialog.getByRole('button', { name: 'Delete space' })).toBeEnabled()
    expect(await page.evaluate(measureLayout)).toEqual([])
    await page.screenshot({ path: `${SCREENSHOTS}/settings-spaces-delete-${width}.png` })
  })
}
