import { expect, test, type Page } from '@playwright/test'

import { serveFixtures } from './api'
import { TODAY } from './fixtures/dates'

async function openOtherSpend(page: Page, width: number, height: number) {
  const missing: string[] = []
  await page.clock.setFixedTime(TODAY)
  await page.addInitScript(() => localStorage.setItem('agentifi.accessToken', 'layout-fixture-token'))
  await serveFixtures(page, missing)
  await page.setViewportSize({ width, height })
  await page.goto('/spending-plan')
  await page.getByText('Other Spend', { exact: true }).first().click()
  const pan = page.locator('.bubbles-pan')
  await expect(pan).toBeVisible()
  await page.waitForLoadState('networkidle')
  expect(missing).toEqual([])
  return pan
}

test.describe('Other Spend bubbles on a phone', () => {
  test('stay inside the page, start on the largest bubble and pan by drag', async ({ page }) => {
    const pan = await openOtherSpend(page, 390, 844)

    const scrolls = async () => ({
      page: await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      ),
      ...(await pan.evaluate((el) => ({ chart: el.scrollLeft, room: el.scrollWidth - el.clientWidth }))),
    })

    await expect.poll(async () => (await scrolls()).room).toBeGreaterThan(0)
    const before = await scrolls()
    expect(before.page).toBeLessThanOrEqual(0)

    const box = (await pan.boundingBox())!
    const y = box.y + 20
    await page.mouse.move(box.x + 200, y)
    await page.mouse.down()
    await page.mouse.move(box.x + 120, y, { steps: 5 })
    await page.mouse.up()

    const after = await scrolls()
    expect(after.chart).toBeGreaterThan(before.chart)
    expect(after.page).toBeLessThanOrEqual(0)
  })

  test('a drag over a bubble does not press it, and a tap does', async ({ page }) => {
    const pan = await openOtherSpend(page, 390, 844)
    const bubble = page.getByRole('button', { name: /^Pets,/ })
    await bubble.scrollIntoViewIfNeeded()

    const target = (await bubble.boundingBox())!
    const x = target.x + target.width / 2
    const y = target.y + target.height / 2
    await page.mouse.move(x, y)
    await page.mouse.down()
    await page.mouse.move(x - 60, y, { steps: 5 })
    await page.mouse.up()
    await expect(bubble).toHaveAttribute('aria-pressed', 'false')

    await pan.evaluate((el) => {
      el.scrollLeft = 0
    })
    await bubble.click()
    await expect(bubble).toHaveAttribute('aria-pressed', 'true')
  })
})
