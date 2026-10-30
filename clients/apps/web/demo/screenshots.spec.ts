import { test, type Locator, type Page } from '@playwright/test'

import { byNumber, SHOWCASE } from './fixtures/ledger'
import { OUTPUT, open, pause, prepare, settle } from './stage'

const DIR = `${OUTPUT}screenshots/`

async function shoot(page: Page, name: string): Promise<void> {
  await settle(page)
  await pause(page, 400)
  await page.mouse.move(0, 0)
  await page.screenshot({ path: `${DIR}${name}.png` })
}

/** Scrolls the page so `target` sits `offset` pixels below the top. */
async function bringToTop(page: Page, target: Locator, offset = 90): Promise<void> {
  const box = await target.first().boundingBox()
  if (box) await page.evaluate((top) => window.scrollBy(0, top), box.y - offset)
}

test.describe('desktop', () => {
  test.use({ viewport: { width: 1440, height: 900 } })

  test('dashboard', async ({ page }) => {
    await prepare(page)
    await open(page, '/')
    await shoot(page, 'dashboard')
  })

  test('transactions', async ({ page }) => {
    await prepare(page)
    await open(page, '/transactions')
    await page.getByRole('button', { name: /4 categories/ }).first().click()
    await bringToTop(page, page.getByText('Reminders', { exact: true }), 80)
    await shoot(page, 'transactions')
  })

  test('assistant', async ({ page }) => {
    await prepare(page)
    await open(page, '/assistant')
    await page.getByRole('textbox').first().fill('Where did our money go this month? And can you tidy up anything uncategorized?')
    await page.getByRole('button', { name: 'Send' }).click()
    await page.getByRole('button', { name: /Accept all 5/ }).waitFor()
    await page.getByText('Where did our money go this month? And can you').first().evaluate((element) => element.scrollIntoView({ block: 'start' }))
    await shoot(page, 'assistant')
  })

  test('costco', async ({ page }) => {
    await prepare(page)
    await open(page, `/transactions?edit=${byNumber(SHOWCASE.costco).id}`)
    await page.getByText('Purchase', { exact: true }).scrollIntoViewIfNeeded()
    await page.getByText('Edit 4 splits').evaluate((element) => element.scrollIntoView({ block: 'start' }))
    await shoot(page, 'costco-receipt')
  })

  test('bills', async ({ page }) => {
    await prepare(page)
    await open(page, '/upcoming')
    await shoot(page, 'bills')
  })

  test('reports', async ({ page }) => {
    await prepare(page)
    await open(page, '/reports')
    await page.locator('.gallery__name', { hasText: /^Spending$/ }).click()
    await shoot(page, 'spending-report')
  })

  test('investments', async ({ page }) => {
    await prepare(page)
    await open(page, '/investing')
    await page.getByRole('tab', { name: 'Performance' }).click()
    await page.getByRole('radio', { name: '6M', exact: true }).click()
    await shoot(page, 'investments')
  })
})

test.describe('phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true })

  test('receipt', async ({ page }) => {
    await prepare(page)
    await open(page, `/transactions?edit=${byNumber(SHOWCASE.pharmacy).id}`)
    await page.getByText('Take photo', { exact: true }).evaluate((element) => element.scrollIntoView({ block: 'center' }))
    await shoot(page, 'phone-receipt')
  })
})
