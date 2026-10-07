import { expect, test, type Page } from '@playwright/test'

import { serveFixtures } from './api'
import { ACCOUNT, ACCOUNTS } from './fixtures/core'
import { TODAY } from './fixtures/dates'

/** The long-named loan with a zero the sync declined to apply. */
async function holdTheLongNamedAccount(page: Page) {
  const accounts = ACCOUNTS.map((one) =>
    one.id === ACCOUNT.mortgage
      ? { ...one, withheld_balance: '0.00', withheld_balance_at: '2026-06-14T17:00:00Z' }
      : one,
  )
  await page.route(
    (url) => url.pathname === '/api/accounts',
    (route) => route.fulfill({ contentType: 'application/json', body: JSON.stringify(accounts) }),
  )
}

async function load(
  page: Page,
  path: string,
  width: number,
  height: number,
  { drawerOpen, before }: { drawerOpen?: boolean; before?: (page: Page) => Promise<void> } = {},
) {
  const missing: string[] = []
  await page.clock.setFixedTime(TODAY)
  await page.addInitScript((open) => {
    localStorage.setItem('agentifi.accessToken', 'layout-fixture-token')
    if (open !== undefined) localStorage.setItem('agentifi.accountsDrawerOpen', String(open))
  }, drawerOpen)
  await serveFixtures(page, missing)
  await before?.(page)
  await page.setViewportSize({ width, height })
  await page.goto(path)
  await page.waitForLoadState('networkidle')
  expect(missing).toEqual([])
}

test('the logo at the top of the rail collapses and expands it, by pointer and by keyboard', async ({ page }) => {
  await load(page, '/', 1440, 900)
  const rail = page.getByRole('navigation', { name: 'Main' })
  const app = page.locator('.app')

  const logo = rail.getByRole('button', { name: 'Collapse sidebar' })
  await expect(logo).toHaveAttribute('aria-expanded', 'true')
  const [mark, nav] = await Promise.all([logo.boundingBox(), rail.boundingBox()])
  expect(mark!.y - nav!.y).toBeLessThan(40)
  expect(mark!.x - nav!.x).toBeLessThan(40)
  const openWidth = nav!.width

  await logo.click()
  await expect(app).toHaveAttribute('data-rail', 'collapsed')
  const expand = rail.getByRole('button', { name: 'Expand sidebar' })
  await expect(expand).toHaveAttribute('aria-expanded', 'false')
  await expect.poll(async () => (await rail.boundingBox())!.width).toBeLessThan(openWidth)
  await page.mouse.move(700, 450)
  await expand.hover()
  await expect(page.getByRole('tooltip')).toContainText('Expand sidebar')

  await expand.focus()
  await page.keyboard.press('Enter')
  await expect(app).not.toHaveAttribute('data-rail', 'collapsed')
  await page.keyboard.press('Space')
  await expect(app).toHaveAttribute('data-rail', 'collapsed')

  await expect(rail.getByRole('button', { name: /sidebar/ })).toHaveCount(1)
})

test('the drawer\'s warning triangle says what it warns about', async ({ page }) => {
  await load(page, '/', 1180, 820, { drawerOpen: true, before: holdTheLongNamedAccount })
  const said =
    "Balance held: SimpleFIN reported $0.00 on Jun 14, 2026, which the account's history doesn't explain. " +
    'The last good balance is shown until you confirm or keep it in Settings › Accounts.'

  const mark = page.locator('.drawer').getByRole('img', { name: said })
  await expect(mark).toBeVisible()
  await mark.hover()
  await expect(page.getByRole('tooltip')).toContainText(said)
})

for (const size of [
  { width: 1180, height: 820 },
  { width: 390, height: 844 },
]) {
  test(`the accounts drawer keeps every row to one line at ${size.width}px`, async ({ page }) => {
    await load(page, '/', size.width, size.height, { drawerOpen: true, before: holdTheLongNamedAccount })
    const drawer = page.locator('.drawer')
    await expect(drawer.locator('.acct-row__name').first()).toBeVisible()

    const rows = await drawer.locator('.acct-row').evaluateAll((all) => {
      const box = document.querySelector('.drawer')!.getBoundingClientRect()
      return all.map((row) => {
        const name = row.querySelector<HTMLElement>('.acct-row__name')
        const amounts = Array.from(row.querySelectorAll<HTMLElement>('.money'))
        const lineHeight = name ? parseFloat(getComputedStyle(name).lineHeight) : 0
        return {
          name: name?.textContent ?? '',
          title: name?.title ?? '',
          oneLine: name ? name.getBoundingClientRect().height <= lineHeight + 1 : true,
          cut: name ? name.scrollWidth > name.clientWidth : false,
          ellipsis: name ? getComputedStyle(name).textOverflow === 'ellipsis' : true,
          amountsWhole: amounts.every((one) => {
            const rect = one.getBoundingClientRect()
            return (
              one.scrollWidth <= one.clientWidth + 1 &&
              rect.left >= box.left &&
              rect.right <= box.right + 1
            )
          }),
        }
      })
    })

    expect(rows.length).toBeGreaterThan(5)
    for (const row of rows) {
      expect(row, row.name).toMatchObject({ oneLine: true, ellipsis: true, amountsWhole: true })
      if (row.name) expect(row.title).toBe(row.name)
    }
    const long = rows.find((row) => row.name === 'Home Mortgage Thirty Year Fixed Rate Loan')
    expect(long?.cut, 'the long name is cut short at this width').toBe(true)
  })
}
