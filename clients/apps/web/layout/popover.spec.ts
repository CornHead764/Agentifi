import { expect, test, type Locator, type Page } from '@playwright/test'

import { serveFixtures } from './api'
import { TODAY } from './fixtures/dates'

/**
 * The register's Filter popover, open at this viewport. With `at`, the page is
 * first scrolled so the trigger's top is that far down the window.
 */
async function openFilter(page: Page, width: number, height: number, at?: number) {
  const missing: string[] = []
  await page.clock.setFixedTime(TODAY)
  await page.addInitScript(() => localStorage.setItem('agentifi.accessToken', 'layout-fixture-token'))
  await serveFixtures(page, missing)
  await page.setViewportSize({ width, height })
  await page.goto('/transactions')
  await page.waitForLoadState('networkidle')
  const trigger = page.getByRole('button', { name: /^Filter/ })
  if (at !== undefined) {
    const top = (await trigger.boundingBox())!.y
    await page.evaluate((by) => window.scrollBy(0, by), top - at)
    await expect.poll(async () => Math.round((await trigger.boundingBox())!.y)).toBe(at)
  }
  await trigger.click()
  const panel = page.locator('.filter-panel')
  await expect(panel).toHaveAttribute('data-placed')
  expect(missing).toEqual([])
  return { trigger, panel }
}

/** How far the panel sits from the trigger on the side it opened on. */
async function placement(trigger: Locator, panel: Locator) {
  const [anchor, box] = await Promise.all([trigger.boundingBox(), panel.boundingBox()])
  const side = await panel.getAttribute('data-side')
  const gap =
    side === 'top' ? anchor!.y - (box!.y + box!.height) : box!.y - (anchor!.y + anchor!.height)
  return { side, gap: Math.round(gap), left: Math.round(box!.x - anchor!.x), height: box!.height }
}

/**
 * Whether the panel is shown whole: inside the window's insets, with neither
 * the panel nor its facet list scrolled, and the footer on screen.
 */
async function whole(panel: Locator) {
  return panel.evaluate((element) => {
    const inset = 12
    const box = element.getBoundingClientRect()
    const footer = element.querySelector('.filter-panel__footer')!.getBoundingClientRect()
    const facets = element.querySelector('.filter-panel__facets')!
    return {
      onScreen: box.top >= inset - 1 && box.bottom <= window.innerHeight - inset + 1,
      footerShown: footer.top >= box.top && footer.bottom <= box.bottom + 1,
      panelScrolls: element.scrollHeight > element.clientHeight + 1,
      facetsScroll: facets.scrollHeight > facets.clientHeight + 1,
    }
  })
}

const WHOLE = { onScreen: true, footerShown: true, panelScrolls: false, facetsScroll: false }

async function settle(page: Page) {
  await page.evaluate(
    () => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))),
  )
}

test('a popover keeps the side it opened on while its search shortens the list', async ({ page }) => {
  // The trigger sits low on a tall phone: the whole list fits only above it,
  // and the searched one fits below it as well.
  const { trigger, panel } = await openFilter(page, 375, 1320)
  await settle(page)
  const opened = await placement(trigger, panel)
  expect(opened.side).toBe('top')

  await panel.getByLabel('Search categories').fill('zzz')
  await expect.poll(async () => (await placement(trigger, panel)).height).toBeLessThan(opened.height)
  await expect
    .poll(() => placement(trigger, panel))
    .toMatchObject({ side: 'top', gap: opened.gap, left: opened.left })
})

test('a popover stays open and on its trigger while the page scrolls under it', async ({ page }) => {
  const { trigger, panel } = await openFilter(page, 1440, 550)
  await settle(page)
  const opened = await placement(trigger, panel)
  const before = (await trigger.boundingBox())!.y

  await page.mouse.move(1300, 520)
  await page.mouse.wheel(0, 200)
  await expect.poll(async () => (await trigger.boundingBox())!.y).toBeLessThan(before)
  await settle(page)

  await expect(panel).toBeVisible()
  await expect
    .poll(() => placement(trigger, panel))
    .toMatchObject({ side: opened.side, gap: opened.gap, left: opened.left })
})

test('a popover hides while its trigger is scrolled out of view', async ({ page }) => {
  const { trigger, panel } = await openFilter(page, 1440, 550)
  await page.mouse.move(1300, 520)
  await page.mouse.wheel(0, 1000)
  await expect.poll(async () => (await trigger.boundingBox())!.y).toBeLessThan(0)
  await expect(panel).toBeHidden()

  await page.mouse.wheel(0, -1000)
  await expect(panel).toBeVisible()
})

test('a popover near the bottom of a short window opens above its trigger, whole', async ({ page }) => {
  const { trigger, panel } = await openFilter(page, 1440, 700, 600)
  await expect.poll(() => placement(trigger, panel)).toMatchObject({ side: 'top', gap: 6 })
  expect(await whole(panel)).toEqual(WHOLE)
})

test('a popover near the top of a short window opens below its trigger, whole', async ({ page }) => {
  const { trigger, panel } = await openFilter(page, 1440, 700, 80)
  await expect.poll(() => placement(trigger, panel)).toMatchObject({ side: 'bottom', gap: 6 })
  expect(await whole(panel)).toEqual(WHOLE)
})

test('a popover with room on neither side covers its trigger rather than shrinking', async ({
  page,
}) => {
  const { trigger, panel } = await openFilter(page, 1440, 620, 290)
  await expect.poll(async () => (await placement(trigger, panel)).gap).toBeLessThan(0)
  expect(await whole(panel)).toEqual(WHOLE)
})

for (const at of [80, 330, 600]) {
  test(`a popover on a phone is shown whole with its trigger ${at}px down`, async ({ page }) => {
    const { panel } = await openFilter(page, 375, 700, at)
    await expect.poll(() => whole(panel)).toEqual(WHOLE)
  })
}

test('a window shorter than the popover shortens its body, not its footer', async ({ page }) => {
  const { panel } = await openFilter(page, 1440, 420, 200)
  await expect
    .poll(() => whole(panel))
    .toMatchObject({ onScreen: true, footerShown: true, panelScrolls: false })
})
