import type { Page } from '@playwright/test'

import { byNumber, SHOWCASE } from './fixtures/ledger'
import { state } from './fixtures/state'
import { receiptHtml, renderHtml, type Card } from './media'
import { glide, pause, press, scrollBy, settle, typeInto } from './stage'

export interface Scene {
  name: string
  card: Card
  path: string
  /** The clicks a viewer watches, from a page that has finished loading. */
  play: (page: Page) => Promise<void>
}

export const OPENING: Card = {
  kind: 'open',
  title: 'Personal finance on your own server.',
  caption: 'A self-hosted manager modelled on Quicken Simplifi, with an assistant that proposes and never acts alone.',
}

export const CLOSING: Card = {
  kind: 'close',
  title: 'Free software. Your data stays home.',
  caption: 'GPL-3.0-or-later · docker compose up -d · github.com/CornHead764/Agentifi',
}

async function sweep(page: Page, selector: string, from = 0.08, to = 0.92, ms = 1800): Promise<void> {
  const box = await page.locator(selector).first().boundingBox()
  if (!box) return
  const y = box.y + box.height * 0.55
  await page.mouse.move(box.x + box.width * from, y, { steps: 20 })
  const steps = Math.round(ms / 30)
  for (let step = 1; step <= steps; step += 1) {
    await page.mouse.move(box.x + box.width * (from + ((to - from) * step) / steps), y)
    await page.waitForTimeout(30)
  }
}

export const SCENES: readonly Scene[] = [
  {
    name: 'dashboard',
    card: { title: 'The whole picture, at a glance', caption: 'Net worth, the month’s plan, bills due and what needs review, on one dashboard.' },
    path: '/',
    play: async (page) => {
      await pause(page, 700)
      await sweep(page, '.recharts-surface', 0.1, 0.95, 1300)
      await pause(page, 500)
      await scrollBy(page, 480, 1100)
      await pause(page, 900)
      await glide(page, page.getByText('Summer trip').first())
      await pause(page, 600)
      await scrollBy(page, 560, 1100)
      await pause(page, 1100)
    },
  },
  {
    name: 'net-worth',
    card: { title: 'Net worth and every account', caption: 'Checking to the 401(k), the house and the car loan, synced through SimpleFIN.' },
    path: '/net-worth',
    play: async (page) => {
      await pause(page, 600)
      await sweep(page, '.recharts-surface', 0.05, 0.98, 2200)
      await pause(page, 400)
      await scrollBy(page, 430, 1100)
      await press(page, page.getByText('Investments', { exact: true }).last(), 1600)
    },
  },
  {
    name: 'register',
    card: { title: 'One register, one filter, real rules', caption: 'The same filter drives the register, reports and watchlists. Rules tidy rows as they arrive.' },
    path: '/transactions',
    play: async (page) => {
      await pause(page, 700)
      await press(page, page.getByRole('button', { name: 'Filter', exact: true }), 700)
      await press(page, page.getByRole('checkbox', { name: 'Food & Dining' }), 500)
      await press(page, page.getByRole('button', { name: 'Apply' }), 1200)
      await scrollBy(page, 300, 800)
      await pause(page, 500)
      await press(page, page.getByRole('link', { name: 'Rules' }), 400)
      await settle(page)
      await glide(page, page.getByText('Pharmacy is HSA eligible'))
      await pause(page, 1300)
    },
  },
  {
    name: 'suggest',
    card: { title: 'Suggested categories, in bulk', caption: 'Pick the uncategorized rows and ask. Each one comes back as a suggestion to approve.' },
    path: '/transactions?uncategorized=1&datePreset=this-month',
    play: async (page) => {
      await pause(page, 600)
      await scrollBy(page, 330, 700)
      await press(page, page.getByRole('checkbox', { name: /Select all/ }).first(), 500)
      await press(page, page.getByRole('button', { name: 'Suggest categories' }).first(), 3200)
      const approve = page.getByRole('button', { name: /^Approve/ })
      await press(page, approve.first(), 900)
      await press(page, approve.first(), 1400)
    },
  },
  {
    name: 'assistant',
    card: { title: 'Ask the assistant', caption: 'It reads the ledger, answers, and proposes changes as cards. Nothing applies until you accept.' },
    path: '/assistant',
    play: async (page) => {
      await pause(page, 500)
      const box = page.getByRole('textbox').first()
      await press(page, box, 200)
      await typeInto(box, 'Where did our money go this month? And can you tidy up anything uncategorized?', 14)
      await press(page, page.getByRole('button', { name: 'Send' }), 2900)
      await scrollBy(page, 520, 1100)
      await pause(page, 1100)
      await press(page, page.getByRole('button', { name: /Accept all 5/ }), 1500)
    },
  },
  {
    name: 'merchants',
    card: { title: 'Amazon and Costco, down to the item', caption: 'Orders are matched to their card charges and split across categories by what was bought.' },
    path: '/transactions',
    play: async (page) => {
      await pause(page, 500)
      const search = page.getByPlaceholder('Search transactions')
      await press(page, search, 200)
      await typeInto(search, 'costco', 45)
      await pause(page, 900)
      await press(page, page.getByRole('button', { name: /4 categories/ }).first(), 1400)
      await page.goto(`/transactions?edit=${byNumber(SHOWCASE.costco).id}`)
      await settle(page)
      await pause(page, 500)
      const purchase = page.getByText('Purchase', { exact: true })
      await purchase.scrollIntoViewIfNeeded()
      await glide(page, purchase)
      await scrollBy(page, 420, 1100)
      await pause(page, 1400)
    },
  },
  {
    name: 'bills',
    card: { title: 'Bills, income and what’s coming', caption: 'Recurring items, fetched statements, autopay tracking and pay-manually reminders.' },
    path: '/upcoming',
    play: async (page) => {
      await pause(page, 700)
      await glide(page, page.getByText('Example Medical Group').first())
      await pause(page, 1600)
      await press(page, page.getByRole('tab', { name: 'Cash flow' }), 400)
      await settle(page)
      await sweep(page, '.recharts-surface', 0.05, 0.95, 1800)
      await pause(page, 900)
    },
  },
  {
    name: 'receipts',
    card: { title: 'HSA receipts, never lost', caption: 'Accounts that need receipts flag every purchase without one. Attach a file or snap a photo.' },
    path: '/transactions?missingReceipt=1&datePreset=all-time',
    play: async (page) => {
      state.receiptImage = await receiptBytes(page)
      await pause(page, 600)
      await scrollBy(page, 300, 700)
      await press(page, page.getByRole('button', { name: 'Needs a receipt: add one' }).first(), 800)
      const add = page.getByText('Add a receipt', { exact: true })
      await add.scrollIntoViewIfNeeded()
      await glide(page, add)
      await pause(page, 300)
      const chooser = page.waitForEvent('filechooser')
      await page.mouse.down()
      await page.mouse.up()
      await (await chooser).setFiles({ name: 'receipt-photo.png', mimeType: 'image/png', buffer: state.receiptImage })
      await pause(page, 1200)
      await glide(page, page.getByRole('button', { name: /^Open receipt/ }).first())
      await pause(page, 1800)
    },
  },
  {
    name: 'reports',
    card: { title: 'Reports and the spending plan', caption: 'Compare months, see where it went, and know what’s left to spend.' },
    path: '/reports',
    play: async (page) => {
      await pause(page, 400)
      await press(page, page.locator('.gallery__name', { hasText: /^Spending$/ }), 400)
      await settle(page)
      await sweep(page, '.recharts-surface', 0.05, 0.97, 1200)
      await scrollBy(page, 520, 1000)
      await pause(page, 900)
      await press(page, page.getByRole('link', { name: 'Spending Plan' }), 300)
      await settle(page)
      await press(page, page.getByText('Other Spend', { exact: true }).first(), 1600)
    },
  },
  {
    name: 'investing',
    card: { title: 'Investments', caption: 'Holdings, allocation and performance across the brokerage and the 401(k).' },
    path: '/investing',
    play: async (page) => {
      await pause(page, 700)
      await glide(page, page.getByText('XMPL').first())
      await pause(page, 900)
      await press(page, page.getByRole('tab', { name: 'Performance' }), 400)
      await press(page, page.getByRole('radio', { name: '6M', exact: true }), 400)
      await settle(page)
      await sweep(page, '.recharts-surface', 0.05, 0.97, 1800)
      await pause(page, 1200)
    },
  },
]

let receipt: Buffer | null = null

/** The receipt photo, rendered once in a page of its own. */
export async function receiptBytes(page: Page): Promise<Buffer> {
  if (receipt) return receipt
  const scratch = await page.context().newPage()
  receipt = await renderHtml(scratch, receiptHtml(), { width: 480, height: 640 })
  await scratch.close()
  return receipt
}
