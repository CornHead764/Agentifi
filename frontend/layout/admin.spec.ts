import { expect, test, type Page } from '@playwright/test'

import { serveFixtures } from './api'
import { TODAY } from './fixtures/dates'

/** How much of its card's content box an element spans, as a fraction. */
async function spanOfCard(page: Page, card: string, selector: string) {
  return page
    .locator('.card')
    .filter({ has: page.getByRole('heading', { name: card, exact: true }) })
    .first()
    .evaluate((box, inner) => {
      const style = getComputedStyle(box)
      const content =
        box.getBoundingClientRect().width - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
      const element = box.querySelector(inner)
      return element ? element.getBoundingClientRect().width / content : 0
    }, selector)
}

test('Server admin\'s backups and single sign-on use the width of their cards', async ({ page }) => {
  const missing: string[] = []
  await page.clock.setFixedTime(TODAY)
  await page.addInitScript(() => localStorage.setItem('agentifi.accessToken', 'layout-fixture-token'))
  await serveFixtures(page, missing)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/settings/admin')
  await page.waitForLoadState('networkidle')
  expect(missing).toEqual([])
  await expect(page.getByRole('heading', { name: 'Single sign-on', exact: true })).toBeVisible()

  expect(await spanOfCard(page, 'Backups', '.table')).toBeGreaterThan(0.95)
  expect(await spanOfCard(page, 'Backups', '.admin-keys')).toBeGreaterThan(0.95)
  expect(await spanOfCard(page, 'Backups', '.setting-row')).toBeGreaterThan(0.95)
  expect(await spanOfCard(page, 'Single sign-on', '.setting-row')).toBeGreaterThan(0.95)
  // A value shown in a field is one line tall, not the width its row would take.
  const directory = page.locator('code.admin-secret__value', { hasText: '/backups' })
  expect((await directory.boundingBox())!.height).toBeLessThan(60)
  // A typed field keeps a form's width rather than running the card's.
  expect(await spanOfCard(page, 'Single sign-on', '.settings__form')).toBeLessThan(0.6)
})
