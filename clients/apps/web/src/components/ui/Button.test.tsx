import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { IconButton } from './Button'

describe('<IconButton>', () => {
  it('is a square secondary button named by its label', () => {
    const markup = renderToStaticMarkup(
      <IconButton label="Close">
        <svg />
      </IconButton>,
    )
    expect(markup).toBe(
      '<button type="button" class="btn btn--secondary btn--icon" aria-label="Close"><svg></svg></button>',
    )
  })

  it('takes the small size and a pressed state', () => {
    const markup = renderToStaticMarkup(
      <IconButton size="sm" variant="ghost" label="Flag this transaction" aria-pressed>
        <svg />
      </IconButton>,
    )
    expect(markup).toContain('class="btn btn--ghost btn--sm btn--icon"')
    expect(markup).toContain('aria-pressed="true"')
  })
})
