import type { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { DUPLICATES_KEY, type DuplicateList } from '@/lib/clients/duplicates'
import { renderScreen, testQueryClient } from '@/test/renderScreen'
import { DuplicatesStrip } from './DuplicatesStrip'

function render(seed?: (client: QueryClient) => void) {
  const client = testQueryClient()
  seed?.(client)
  return renderScreen(<DuplicatesStrip />, { client })
}

const waiting = (count: number): DuplicateList => ({ count, pairs: [] })

describe('the duplicates notice above the register', () => {
  it('is absent until the count is known, so the register does not jump', () => {
    expect(render()).not.toContain('possible duplicate')
  })

  it('is absent when nothing waits', () => {
    expect(render((client) => client.setQueryData(DUPLICATES_KEY, waiting(0)))).not.toContain(
      'possible duplicate',
    )
  })

  it('names how many pairs wait and links to the review list', () => {
    const markup = render((client) => client.setQueryData(DUPLICATES_KEY, waiting(3)))
    expect(markup).toContain('3 possible duplicates')
    expect(markup).toContain('href="/settings/duplicates"')
    expect(markup).toContain('Review')
  })

  it('says the plural correctly for one', () => {
    const markup = render((client) => client.setQueryData(DUPLICATES_KEY, waiting(1)))
    expect(markup).toContain('1 possible duplicate found')
  })
})
