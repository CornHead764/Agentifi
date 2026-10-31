import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { Spinner } from './Spinner'

describe('<Spinner>', () => {
  it('is decoration by default, at the size of body text', () => {
    const markup = renderToStaticMarkup(<Spinner />)
    expect(markup).toMatch(/class="[^"]*\bspinner\b/)
    expect(markup).toContain('aria-hidden="true"')
    expect(markup).toContain('width="14"')
  })

  it('is an image with a name when it is given a label', () => {
    const markup = renderToStaticMarkup(<Spinner size={12} label="Applying" />)
    expect(markup).toContain('role="img"')
    expect(markup).toContain('aria-label="Applying"')
    expect(markup).not.toContain('aria-hidden')
  })
})
