import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { QrImage } from './QrImage'

const URI = 'otpauth://totp/Agentifi:ada@example.test?secret=JBSWY3DPEHPK3PXP&issuer=Agentifi'

describe('the QR image', () => {
  it('draws the symbol on a white plate with its quiet zone', () => {
    const markup = renderToStaticMarkup(<QrImage value={URI} label="Scan this" />)
    // A 78-byte URI needs version 5 at level M, which is 37 modules, plus four either side.
    expect(markup).toContain('viewBox="0 0 45 45"')
    expect(markup).toContain('fill="#ffffff"')
    expect(markup).toContain('aria-label="Scan this"')
    expect(markup).toContain('role="img"')
  })

  it('leaves the fallback alone when the value is too long to encode', () => {
    const markup = renderToStaticMarkup(<QrImage value={'x'.repeat(3000)} label="Scan this" />)
    expect(markup).toBe('')
  })
})
