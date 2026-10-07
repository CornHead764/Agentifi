import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { EmptyState } from './EmptyState'

describe('<EmptyState>', () => {
  it('is a centred block with its icon by default', () => {
    const markup = renderToStaticMarkup(<EmptyState icon={<svg />} title="No accounts yet" />)
    expect(markup).toContain('class="empty"')
    expect(markup).toContain('class="empty__icon"')
  })

  it('is one quiet line when compact, and draws no icon', () => {
    const markup = renderToStaticMarkup(
      <EmptyState compact icon={<svg />} title="No passkeys enrolled yet." body="Add one below." />,
    )
    expect(markup).toContain('class="empty empty--compact"')
    expect(markup).not.toContain('empty__icon')
    expect(markup).toContain(
      '<p class="empty__title">No passkeys enrolled yet.</p><p class="empty__body">Add one below.</p>',
    )
  })
})
