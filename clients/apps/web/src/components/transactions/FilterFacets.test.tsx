/**
 * The one filter editor's Advanced facets. The attachment question is offered
 * wherever a filter is read over stored rows, and withheld from the rule,
 * guidance and trigger builders, whose conditions the server refuses it in.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { EMPTY_DRAFT, type FilterDraft } from '@/lib/transactions/filter'

import { FilterFacets } from './FilterFacets'

function advanced(draft: FilterDraft, offerVerdictFacets?: boolean): string {
  return renderToStaticMarkup(
    <FilterFacets
      draft={draft}
      categories={[]}
      tags={[]}
      accounts={[]}
      facets={['advanced']}
      offerVerdictFacets={offerVerdictFacets}
      onChange={() => {}}
    />,
  )
}

/** The radio row whose label is `label`, as markup. */
function radioRow(html: string, label: string): string {
  const rows = html.split('control-row"')
  const row = rows.find((one) => one.includes(`>${label}</label>`))
  expect(row, `no radio labelled ${label}`).toBeDefined()
  return row ?? ''
}

describe('the attachments facet', () => {
  it('asks any, has attachments or no attachments', () => {
    const html = advanced(EMPTY_DRAFT)
    expect(html).toContain('>Attachments</p>')
    expect(radioRow(html, 'Has attachments')).toContain('aria-checked="false"')
    expect(radioRow(html, 'No attachments')).toContain('aria-checked="false"')
  })

  it('shows the chosen answer', () => {
    const attached = advanced({ ...EMPTY_DRAFT, hasAttachment: true })
    expect(radioRow(attached, 'Has attachments')).toContain('aria-checked="true"')
    expect(radioRow(attached, 'No attachments')).toContain('aria-checked="false"')

    const bare = advanced({ ...EMPTY_DRAFT, hasAttachment: false })
    expect(radioRow(bare, 'No attachments')).toContain('aria-checked="true"')
    expect(radioRow(bare, 'Has attachments')).toContain('aria-checked="false"')
  })

  it('is withheld where a row is judged as it arrives', () => {
    const html = advanced(EMPTY_DRAFT, false)
    expect(html).not.toContain('Attachments')
    expect(html).toContain('>Review</p>')
  })
})

describe('the receipts facet', () => {
  it('asks any, missing a receipt or not', () => {
    const owed = advanced({ ...EMPTY_DRAFT, missingReceipt: true })
    expect(owed).toContain('>Receipts</p>')
    expect(radioRow(owed, 'Missing a receipt')).toContain('aria-checked="true"')
    expect(radioRow(owed, 'Not missing one')).toContain('aria-checked="false"')
  })

  it('is withheld where a row is judged as it arrives', () => {
    expect(advanced(EMPTY_DRAFT, false)).not.toContain('Receipts')
  })
})

describe('a facet whose every id was deleted', () => {
  function facet(draft: FilterDraft, id: 'categories' | 'tags' | 'accounts'): string {
    return renderToStaticMarkup(
      <FilterFacets
        draft={draft}
        categories={[]}
        tags={[]}
        accounts={[]}
        facets={[id]}
        onChange={() => {}}
      />,
    )
  }
  const deleted = { ids: [], negated: false, deleted: true }

  it('says what it names and offers to remove it', () => {
    const html = facet({ ...EMPTY_DRAFT, categories: deleted }, 'categories')
    expect(html).toContain('This filter names a deleted category, so it matches nothing.')
    expect(html).toContain('>Remove</button>')
    expect(facet({ ...EMPTY_DRAFT, accounts: { ...deleted, negated: true } }, 'accounts')).toContain(
      'This filter excludes a deleted account, which excludes nothing.',
    )
  })

  it('says nothing once an id is chosen, or for a facet never set', () => {
    expect(facet({ ...EMPTY_DRAFT, tags: { ...deleted, ids: ['t1'] } }, 'tags')).not.toContain('deleted')
    expect(facet(EMPTY_DRAFT, 'tags')).not.toContain('deleted')
  })
})
