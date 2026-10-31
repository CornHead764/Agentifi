/**
 * Statement is a button, not an anchor: the browser navigates an `href` to
 * `/documents/{id}/content` with no bearer token, which is a 401 in a fresh
 * tab. So the assertion is on the element.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { ToastProvider } from '@/components/ui'

import { StatementButton } from './StatementButton'

function render(): string {
  return renderToStaticMarkup(
    <ToastProvider>
      <StatementButton documentId="doc-1" />
    </ToastProvider>,
  )
}

describe('the Statement control', () => {
  it('is a button rather than a link the browser would follow unauthenticated', () => {
    const markup = render()

    expect(markup).toContain('<button')
    expect(markup).toContain('type="button"')
    expect(markup).not.toContain('href')
  })

  it('says the whole word, so nothing has to be truncated to fit', () => {
    expect(render()).toContain('Statement')
  })
})
