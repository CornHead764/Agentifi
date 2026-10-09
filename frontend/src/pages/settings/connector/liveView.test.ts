import { describe, expect, it } from 'vitest'

import { pagePixels } from './liveView'

describe('a click on the drawn page', () => {
  it('lands at the page pixel under it, however small the page is drawn', () => {
    // A 1280 by 720 page drawn at half size, 100px from the dialog's left edge.
    const drawn = { left: 100, top: 50, width: 640, height: 360 }
    const page = { width: 1280, height: 720 }
    expect(pagePixels(drawn, page, { x: 100, y: 50 })).toEqual({ x: 0, y: 0 })
    expect(pagePixels(drawn, page, { x: 306, y: 155 })).toEqual({ x: 412, y: 210 })
    expect(pagePixels(drawn, page, { x: 740, y: 410 })).toEqual({ x: 1280, y: 720 })
  })

  it('sends nothing for a page with no size', () => {
    const drawn = { left: 0, top: 0, width: 640, height: 360 }
    expect(pagePixels(drawn, { width: 0, height: 0 }, { x: 5, y: 5 })).toBeNull()
    expect(pagePixels({ ...drawn, width: 0 }, { width: 1280, height: 720 }, { x: 5, y: 5 })).toBeNull()
  })
})
