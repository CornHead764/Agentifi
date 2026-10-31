import { mkdirSync, writeFileSync } from 'node:fs'

import { test } from '@playwright/test'

import { SCENES } from './scenes'
import { OUTPUT, open, prepare } from './stage'

const VIEWPORT = { width: 1600, height: 900 }
const DIR = `${OUTPUT}video/`

test.use({ viewport: VIEWPORT, video: { mode: 'on', size: VIEWPORT } })

/**
 * One recording per scene. The page loads before the scene starts, so the
 * clip's first `start` seconds are trimmed off when the film is assembled.
 */
SCENES.forEach((scene, index) => {
  test(`scene ${scene.name}`, async ({ page }) => {
    const began = Date.now()
    const missing = await prepare(page, { cursor: true })
    await open(page, scene.path)
    await page.mouse.move(VIEWPORT.width * 0.62, VIEWPORT.height * 0.55)
    await page.waitForTimeout(250)
    const start = (Date.now() - began) / 1000
    await scene.play(page)
    const length = (Date.now() - began) / 1000 - start
    const video = page.video()
    await page.close()
    const stem = `${DIR}${String(index + 1).padStart(2, '0')}-${scene.name}`
    mkdirSync(DIR, { recursive: true })
    await video?.saveAs(`${stem}.webm`)
    writeFileSync(`${stem}.json`, JSON.stringify({ start, length, missing: [...new Set(missing)] }, null, 2))
  })
})
