import { expect, test, type Page } from '@playwright/test'

import { serveFixtures } from './api'
import { ACCOUNT, ACCOUNTS } from './fixtures/core'
import { TODAY } from './fixtures/dates'

/** The register at `path`, with the fixtures answering and nothing missing. */
async function open(page: Page, path: string, width = 1440) {
  const missing: string[] = []
  await page.clock.setFixedTime(TODAY)
  await page.addInitScript(() => localStorage.setItem('agentifi.accessToken', 'layout-fixture-token'))
  await serveFixtures(page, missing)
  await page.setViewportSize({ width, height: 900 })
  await page.goto(path)
  await page.waitForLoadState('networkidle')
  expect(missing).toEqual([])
}

test('a chart wedge narrows the register with a way back out', async ({ page }) => {
  await open(page, `/transactions?displayNode=${ACCOUNT.checking}&tab=spending`)
  const breadcrumb = page.getByRole('navigation', { name: 'Chart breakdown' })
  await expect(breadcrumb).toBeHidden()

  const wedge = page.locator('.agg-legend__row[role="button"]').first()
  const name = (await wedge.locator('.agg-legend__name').innerText()).trim()
  await wedge.click()

  await expect(breadcrumb).toBeVisible()
  await expect(breadcrumb).toContainText(name)
  const chip = page.getByRole('button', { name: `Remove ${name} from the filter` })
  await expect(chip).toBeVisible()
  expect(new URL(page.url()).searchParams.getAll('drill')).toHaveLength(1)

  // The browser's Back steps out of the drill.
  await page.goBack()
  await expect(breadcrumb).toBeHidden()
  await expect(chip).toBeHidden()
  expect(new URL(page.url()).searchParams.getAll('drill')).toEqual([])

  // So does taking the chip off, and the breadcrumb's root.
  await wedge.click()
  await expect(chip).toBeVisible()
  await chip.click()
  await expect(breadcrumb).toBeHidden()
  await wedge.click()
  await breadcrumb.getByRole('button', { name: 'All' }).click()
  await expect(chip).toBeHidden()
})

test('the account picker leaves a small balance out until it is revealed', async ({ page }) => {
  const flagged = ACCOUNTS.map((row) =>
    row.id === ACCOUNT.brokerage ? { ...row, hidden_small_balance: true } : row,
  )
  const brokerage = flagged.find((row) => row.id === ACCOUNT.brokerage)!.name
  // Wide enough for the register's columns beside the open accounts drawer.
  await open(page, '/transactions?displayNode=all', 2400)
  await page.route('**/api/accounts', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(flagged) }),
  )
  await page.reload()
  await page.waitForLoadState('networkidle')

  const cell = page.getByRole('button', { name: /^Account: / }).first()
  await cell.scrollIntoViewIfNeeded()
  await cell.click()
  const picker = page.locator('.picker')
  await expect(picker).toBeVisible()
  await expect(picker.getByRole('button', { name: brokerage })).toBeHidden()

  await picker.getByRole('button', { name: '1 small balance hidden' }).click()
  await expect(picker.getByRole('button', { name: brokerage })).toBeVisible()
  await expect(picker.getByRole('button', { name: 'Hide 1 small balance' })).toBeVisible()
})

test('an account select reveals its small balances and stays open', async ({ page }) => {
  const flagged = ACCOUNTS.map((row) =>
    row.id === ACCOUNT.savings ? { ...row, hidden_small_balance: true } : row,
  )
  const savings = flagged.find((row) => row.id === ACCOUNT.savings)!.name
  await open(page, '/goals')
  await page.route('**/api/accounts', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(flagged) }),
  )
  await page.reload()
  await page.waitForLoadState('networkidle')

  await page.getByRole('button', { name: 'New goal' }).click()
  await page.locator('.goal-template').first().click()
  const select = page.getByRole('combobox', { name: 'Reserve in account' })
  await select.click()
  const list = page.getByRole('listbox')
  await expect(list.getByRole('option', { name: savings })).toBeHidden()

  await list.getByRole('option', { name: '1 small balance hidden' }).click()
  await expect(list).toBeVisible()
  await list.getByRole('option', { name: savings }).click()
  await expect(select).toHaveText(savings)
})
