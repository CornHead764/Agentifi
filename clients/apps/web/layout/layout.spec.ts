import { expect, test, type Page } from '@playwright/test'

import { DESTINATIONS, SETTINGS_SECTIONS } from '../src/components/shell/destinations'
import { ALLOWED } from './allowlist'
import { serveFixtures } from './api'
import { measureLayout, type Violation } from './checks'
import { TODAY } from './fixtures/dates'
import { FONT_CSS } from './fonts'
import { ROUTES, WIDTHS, type LayoutRoute, type LayoutWidth } from './routes'

const SCREENSHOTS = new URL('./output/screenshots/', import.meta.url).pathname

test('every destination and Settings section has a route here', () => {
  const covered = new Set(ROUTES.map((route) => route.path))
  const wanted = [...DESTINATIONS.map((one) => one.path), ...SETTINGS_SECTIONS.map((one) => one.path)]
  expect(wanted.filter((path) => !covered.has(path))).toEqual([])
})

/** Until the page stops fetching and nothing on it is a placeholder. */
async function settle(page: Page): Promise<void> {
  await page.waitForLoadState('networkidle')
  await page
    .locator('.skeleton, .spinner, [aria-busy="true"]')
    .first()
    .waitFor({ state: 'detached', timeout: 10_000 })
    .catch(() => undefined)
  await page.evaluate(async () => {
    await document.fonts.ready
    await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)))
  })
}

async function visit(page: Page, route: LayoutRoute, size: LayoutWidth): Promise<Violation[]> {
  const found: Violation[] = []
  const missing: string[] = []

  page.on('console', (message) => {
    if (message.type() !== 'error') return
    const where = message.location().url
    // A fixture that answers an error on purpose is logged by the browser too.
    if (message.text().startsWith('Failed to load resource') && where.includes('/api/')) return
    found.push({ check: 'console', element: 'console', detail: message.text().slice(0, 300) })
  })
  page.on('pageerror', (error) => {
    found.push({ check: 'console', element: 'uncaught', detail: error.message.slice(0, 300) })
  })

  await page.clock.setFixedTime(TODAY)
  await page.addInitScript(
    ({ drawerOpen }) => {
      localStorage.setItem('agentifi.accessToken', 'layout-fixture-token')
      if (drawerOpen !== undefined) localStorage.setItem('agentifi.accountsDrawerOpen', String(drawerOpen))
    },
    { drawerOpen: size.drawerOpen },
  )
  await serveFixtures(page, missing)
  await page.setViewportSize({ width: size.width, height: size.height })

  await page.goto(route.path)
  await page.addStyleTag({ content: FONT_CSS })
  await settle(page)
  if (route.open) {
    await route.open(page)
    await settle(page)
  }

  const pinned = await page.evaluate(() =>
    Array.from(document.fonts).some((face) => face.family.includes('Inter') && face.status === 'loaded'),
  )
  expect(pinned, 'the pinned Inter face did not load; see layout/fonts.ts').toBe(true)

  found.push(...(await page.evaluate(measureLayout)))
  for (const request of new Set(missing)) {
    found.push({ check: 'fixtures', element: request, detail: 'no fixture in layout/fixtures answers this request' })
  }

  // Not `fullPage`: its capture passes through a frame where the docked
  // drawer floats, and the register and the charts measure themselves against
  // that frame. A viewport as tall as the page is laid out like the one checked.
  const tall = await page.evaluate(() => document.documentElement.scrollHeight)
  await page.setViewportSize({ width: size.width, height: Math.max(size.height, tall) })
  await settle(page)
  await page.screenshot({ path: `${SCREENSHOTS}${size.width}/${route.name.replaceAll('/', '__')}.png` })
  return found
}

function format(route: LayoutRoute, width: number, violation: Violation): string {
  return `[${route.name} @ ${width}px] ${violation.check}: ${violation.element} — ${violation.detail}`
}

for (const size of WIDTHS) {
  test.describe(`${size.width}px`, () => {
    for (const route of ROUTES) {
      test(route.name, async ({ page }) => {
        const found = await visit(page, route, size)
        const allowed = ALLOWED.filter(
          (entry) => entry.route === route.name && entry.widths.includes(size.width),
        )
        const unexpected = found.filter(
          (violation) =>
            violation.check === 'fixtures' || !allowed.some((entry) => entry.check === violation.check),
        )
        const stale = allowed.filter((entry) => !found.some((violation) => violation.check === entry.check))

        const problems = [
          ...unexpected.map((violation) => format(route, size.width, violation)),
          ...stale.map(
            (entry) =>
              `[${route.name} @ ${size.width}px] ${entry.check}: allowlisted in layout/allowlist.ts but not found; drop ${size.width} from the entry`,
          ),
        ]
        const tolerated = found.filter((violation) => !unexpected.includes(violation))
        if (tolerated.length > 0) {
          test.info().annotations.push({
            type: 'allowlisted',
            description: tolerated.map((violation) => format(route, size.width, violation)).join('\n'),
          })
        }
        expect(problems, problems.join('\n')).toEqual([])
      })
    }
  })
}
