/**
 * The guidance editor's left column is the one filter editor, the facets the
 * register's Filter popover mounts. A structural test, because both columns
 * type-check with the facets left unmounted.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import type { Account, Category, Tag, Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { GuidanceEditor } from './GuidanceEditor'

const ACCOUNTS = [{ id: 'a-1' as Uuid, name: 'Everyday Checking' }] as unknown as Account[]
const CATEGORIES = [
  { id: 'c-1' as Uuid, name: 'Fast Food', parent_id: null, is_user_assignable: true },
] as unknown as Category[]
const TAGS = [{ id: 't-1' as Uuid, name: 'Roadtrip' }] as unknown as Tag[]

function render(): string {
  return renderScreen(
    <GuidanceEditor
      note={null}
      accounts={ACCOUNTS}
      categories={CATEGORIES}
      tags={TAGS}
      pending={false}
      onSubmit={() => Promise.resolve()}
      onClose={() => {}}
    />,
  )
}

describe('the guidance editor', () => {
  it('asks which name to match on, how, and with what', () => {
    const markup = render()
    expect(markup).toContain('Match on')
    expect(markup).toContain('Comparison')
    expect(markup).toContain('Keywords')
  })

  it('offers the register‘s own facets beside the keywords', () => {
    // Mixing fields is the point: a note about a shop, narrowed to an account,
    // to an amount, to a category — all from the one filter editor.
    const markup = render()
    for (const facet of ['Categories', 'Tags', 'Accounts', 'Flags', 'Amount', 'Advanced']) {
      expect(markup).toContain(facet)
    }
  })

  it('does not offer the facet the server refuses', () => {
    // Bills & Subscriptions is the series matcher's verdict, and the server
    // refuses it for a rule and a note (422).
    expect(render()).not.toContain('Bills &amp; Subscriptions')
  })

  it('says a note needs a condition before it can be saved', () => {
    expect(render()).toContain('needs at least one condition')
  })
})
