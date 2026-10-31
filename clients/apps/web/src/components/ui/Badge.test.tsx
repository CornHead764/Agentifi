import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { Badge } from './Badge'

describe('<Badge>', () => {
  it('is neutral with no modifier by default', () => {
    expect(renderToStaticMarkup(<Badge>Manual</Badge>)).toBe('<span class="badge">Manual</span>')
  })

  it('takes a tone, the count shape and a class of its own', () => {
    expect(
      renderToStaticMarkup(
        <Badge tone="warning" count className="txn-line__category">
          3
        </Badge>,
      ),
    ).toBe('<span class="badge badge--warning badge--count txn-line__category">3</span>')
  })
})
