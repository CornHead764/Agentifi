import type { Locator, Page } from '@playwright/test'

import { serveFixtures } from './api'
import { TODAY } from './fixtures/dates'
import { FONT_CSS } from './fonts'
import { state } from './fixtures/state'

export const OUTPUT = new URL('./output/', import.meta.url).pathname

/**
 * A pointer drawn into the page, since a headless browser records none. It
 * follows the mouse Playwright moves and pulses on a press.
 */
const CURSOR_SCRIPT = `
(() => {
  const install = () => {
    if (document.getElementById('demo-cursor')) return
    const style = document.createElement('style')
    style.textContent = \`
      #demo-cursor { position: fixed; left: 0; top: 0; width: 26px; height: 26px; z-index: 2147483647;
        pointer-events: none; transform: translate(-100px, -100px); transition: opacity 200ms; }
      #demo-cursor svg { filter: drop-shadow(0 1px 2px rgb(0 0 0 / 0.35)); }
      #demo-cursor .ring { position: absolute; left: -14px; top: -14px; width: 28px; height: 28px; border-radius: 50%;
        background: rgb(108 92 231 / 0.35); transform: scale(0); opacity: 0; }
      #demo-cursor.down .ring { animation: demo-ring 450ms ease-out; }
      @keyframes demo-ring { from { transform: scale(0.3); opacity: 1; } to { transform: scale(1.6); opacity: 0; } }
    \`
    document.head.appendChild(style)
    const cursor = document.createElement('div')
    cursor.id = 'demo-cursor'
    cursor.innerHTML = '<div class="ring"></div><svg width="26" height="26" viewBox="0 0 24 24"><path d="M4 2.5 L4 19 L8.6 14.9 L11.6 21.5 L14.4 20.3 L11.4 13.8 L17.6 13.6 Z" fill="#111" stroke="#fff" stroke-width="1.4" stroke-linejoin="round"/></svg>'
    document.body.appendChild(cursor)
    window.addEventListener('mousemove', (event) => {
      cursor.style.transform = 'translate(' + event.clientX + 'px, ' + event.clientY + 'px)'
    }, true)
    window.addEventListener('mousedown', () => {
      cursor.classList.remove('down')
      void cursor.offsetWidth
      cursor.classList.add('down')
    }, true)
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', install)
  else install()
})()
`

export interface OpenOptions {
  /** The accounts drawer beside the dashboard and the register. */
  drawer?: boolean
  cursor?: boolean
  receiptImage?: Buffer
}

/** Until the page stops fetching and nothing on it is a placeholder. */
export async function settle(page: Page): Promise<void> {
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

/** Signs in as Sam Sample with the clock at the demo's today, and serves the household. */
export async function prepare(page: Page, options: OpenOptions = {}): Promise<string[]> {
  const missing: string[] = []
  page.on('pageerror', (error) => console.log(`[page error] ${error.message}`))
  await page.clock.setFixedTime(TODAY)
  await page.addInitScript(
    ({ drawer }) => {
      localStorage.setItem('agentifi.accessToken', 'demo-token')
      localStorage.setItem('agentifi.theme', 'light')
      localStorage.setItem('agentifi.accountsDrawerOpen', String(drawer))
      localStorage.setItem('agentifi.reminders.collapsed', 'true')
    },
    { drawer: options.drawer ?? false },
  )
  await page.addInitScript({
    content: `document.addEventListener('DOMContentLoaded', () => {
      const style = document.createElement('style')
      style.textContent = ${JSON.stringify(FONT_CSS)}
      document.head.appendChild(style)
    })`,
  })
  if (options.cursor) await page.addInitScript({ content: CURSOR_SCRIPT })
  await serveFixtures(page, missing)
  state.receiptImage = options.receiptImage ?? null
  return missing
}

export async function open(page: Page, path: string): Promise<void> {
  await page.goto(path)
  await settle(page)
}

export function pause(page: Page, ms: number): Promise<void> {
  return page.waitForTimeout(ms)
}

/** Moves the pointer to the middle of `target` the way a hand would, without clicking. */
export async function glide(page: Page, target: Locator, steps = 28): Promise<void> {
  await target.scrollIntoViewIfNeeded()
  const box = await target.boundingBox()
  if (!box) throw new Error(`nothing to point at: ${target}`)
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, { steps })
}

export async function press(page: Page, target: Locator, settleMs = 450): Promise<void> {
  await glide(page, target)
  await pause(page, 180)
  await target.click()
  await pause(page, settleMs)
}

/** Scrolls the page by `distance` pixels over about `ms`, for a recording to follow. */
export async function scrollBy(page: Page, distance: number, ms = 1200): Promise<void> {
  const steps = Math.max(1, Math.round(ms / 16))
  for (let step = 0; step < steps; step += 1) {
    await page.mouse.wheel(0, distance / steps)
    await page.waitForTimeout(16)
  }
}

export async function typeInto(target: Locator, text: string, delay = 28): Promise<void> {
  await target.pressSequentially(text, { delay })
}
