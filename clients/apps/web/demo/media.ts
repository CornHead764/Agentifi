import type { Page } from '@playwright/test'

import { FONT_CSS } from './fonts'

/**
 * Pages the demo draws itself rather than the app: the title cards between
 * scenes and the invented receipt a scene attaches. Served from the dev
 * server's origin so they load the app's own fonts and logo.
 */
const PATH = '/__demo/page'

export async function renderHtml(page: Page, html: string, size: { width: number; height: number }, scale = 1): Promise<Buffer> {
  await page.setViewportSize(size)
  await page.route(`**${PATH}`, (route) => route.fulfill({ contentType: 'text/html', body: html }))
  await page.goto(PATH)
  await page.evaluate(async () => {
    await document.fonts.ready
    await Promise.all(Array.from(document.images, (image) => image.decode().catch(() => undefined)))
  })
  await page.unroute(`**${PATH}`)
  const bytes = await page.screenshot({ scale: scale === 1 ? 'css' : 'device' })
  return bytes
}

function escape(text: string): string {
  return text.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
}

export interface Card {
  title: string
  caption: string
  /** The opening and closing cards carry the name and the link instead of a counter. */
  kind?: 'open' | 'close'
  step?: string
}

/** A title card in the app's palette: the dark surfaces, the indigo accent, Inter. */
export function cardHtml(card: Card): string {
  const big = card.kind !== undefined
  return `<!doctype html><html><head><meta charset="utf-8"><style>
${FONT_CSS}
:root { --surface-0: #0e0f13; --surface-2: #1c1f28; --text: #e8eaf0; --text-muted: #9aa1b2; --accent: #6c5ce7; --accent-fg: #9286ed; }
* { box-sizing: border-box; margin: 0; }
html, body { width: 100%; height: 100%; }
body { background: radial-gradient(1200px 700px at 78% 18%, #6c5ce733, transparent 60%),
  radial-gradient(900px 600px at 12% 95%, #22d3ee1a, transparent 60%), var(--surface-0);
  color: var(--text); font-family: Inter, system-ui, sans-serif; display: grid; place-items: center; }
main { width: 1480px; display: grid; gap: ${big ? 34 : 26}px; }
.brand { display: flex; align-items: center; gap: 22px; }
.brand img { width: ${big ? 104 : 64}px; height: ${big ? 104 : 64}px; border-radius: ${big ? 24 : 15}px; box-shadow: 0 10px 40px #6c5ce755; }
.brand span { font-size: ${big ? 72 : 30}px; font-weight: 700; letter-spacing: -0.02em; color: ${big ? 'var(--text)' : 'var(--text-muted)'}; }
.step { font: 600 24px/1 'SF Mono', monospace; color: var(--accent-fg); letter-spacing: 0.08em; text-transform: uppercase; }
h1 { font-size: ${big ? 64 : 84}px; font-weight: 700; letter-spacing: -0.03em; line-height: 1.05; max-width: 1400px; }
p { font-size: ${big ? 34 : 38}px; line-height: 1.35; color: var(--text-muted); max-width: 1300px; }
.rule { width: 120px; height: 6px; border-radius: 3px; background: var(--accent); }
</style></head><body><main>
<div class="brand"><img src="/icon.svg" alt=""><span>Agentifi</span></div>
${card.step ? `<div class="step">${escape(card.step)}</div>` : ''}
<div class="rule"></div>
<h1>${escape(card.title)}</h1>
<p>${escape(card.caption)}</p>
</main></body></html>`
}

/** An invented pharmacy receipt, photographed on a counter, for the HSA scene. */
export function receiptHtml(): string {
  const lines = [
    ['Allergy relief, 30 ct', '15.00'],
    ['Saline nasal spray', '8.00'],
    ['Adhesive bandages', '2.00'],
  ]
  return `<!doctype html><html><head><meta charset="utf-8"><style>
${FONT_CSS}
* { box-sizing: border-box; margin: 0; }
body { width: 100%; height: 100%; display: grid; place-items: center;
  background: repeating-linear-gradient(90deg, #b9a17f 0 18px, #b19877 18px 36px); }
.paper { width: 330px; padding: 26px 24px 34px; background: #fbfaf6; transform: rotate(-3deg);
  box-shadow: 0 18px 30px #0005; font: 15px/1.5 'SF Mono', monospace; color: #2a2a2a; }
h2 { font: 700 20px/1.2 Inter, sans-serif; text-align: center; letter-spacing: 0.06em; }
.small { text-align: center; font-size: 13px; color: #555; margin: 4px 0 14px; }
.row { display: flex; justify-content: space-between; }
hr { border: 0; border-top: 1px dashed #999; margin: 12px 0; }
.total { font-weight: 700; font-size: 17px; }
.hsa { margin-top: 14px; text-align: center; font-size: 13px; border: 1px solid #2a2a2a; padding: 4px; }
</style></head><body><div class="paper">
<h2>EXAMPLE PHARMACY</h2>
<div class="small">Store #0100 · Springfield<br>06/12/2026 · 14:32</div>
${lines.map(([item, price]) => `<div class="row"><span>${item}</span><span>${price}</span></div>`).join('')}
<hr>
<div class="row total"><span>TOTAL</span><span>25.00</span></div>
<div class="row"><span>HSA CARD ****3333</span><span>25.00</span></div>
<div class="hsa">FSA/HSA ELIGIBLE ITEMS: 25.00</div>
</div></body></html>`
}
