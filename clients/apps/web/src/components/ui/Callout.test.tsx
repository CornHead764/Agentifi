import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { Callout } from './Callout'

describe('<Callout>', () => {
  it('is an info notice with the tone’s glyph by default', () => {
    const markup = renderToStaticMarkup(<Callout>Counted at face value.</Callout>)
    expect(markup).toContain('class="callout callout--info"')
    expect(markup).toContain('class="callout__icon" aria-hidden="true"><svg')
  })

  it('draws no glyph when told not to', () => {
    const markup = renderToStaticMarkup(
      <Callout tone="warning" icon={false}>
        Stopped.
      </Callout>,
    )
    expect(markup).toContain('callout--warning')
    expect(markup).not.toContain('callout__icon')
  })

  it('puts the title above the body and the actions after it', () => {
    const markup = renderToStaticMarkup(
      <Callout tone="expense" title="Sign-in failed" actions={<button type="button">Retry</button>}>
        <p>The password was refused.</p>
      </Callout>,
    )
    expect(markup).toMatch(
      /callout__body"><p class="callout__title">Sign-in failed<\/p><p>The password was refused\.<\/p><\/div><div class="callout__actions"><button/,
    )
  })

  it('passes a role through, for a notice that changes while on screen', () => {
    expect(renderToStaticMarkup(<Callout role="status">Checking</Callout>)).toContain('role="status"')
  })
})
