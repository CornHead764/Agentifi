import { mkdirSync, writeFileSync } from 'node:fs'

import { test } from '@playwright/test'

import { cardHtml, renderHtml } from './media'
import { CLOSING, OPENING, SCENES } from './scenes'
import { OUTPUT } from './stage'

const DIR = `${OUTPUT}cards/`

test('title cards', async ({ page }) => {
  mkdirSync(DIR, { recursive: true })
  const cards = [
    { name: '00-open', card: OPENING },
    ...SCENES.map((scene, index) => ({
      name: `${String(index + 1).padStart(2, '0')}-${scene.name}`,
      card: { ...scene.card, step: `${index + 1} / ${SCENES.length}` },
    })),
    { name: `${String(SCENES.length + 1).padStart(2, '0')}-close`, card: CLOSING },
  ]
  for (const { name, card } of cards) {
    writeFileSync(`${DIR}${name}.png`, await renderHtml(page, cardHtml(card), { width: 1920, height: 1080 }))
  }
})
