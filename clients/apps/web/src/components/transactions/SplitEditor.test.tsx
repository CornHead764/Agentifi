/**
 * The split grid is part of the transaction form: it has no Save of its own, and
 * what blocks the form's Update is said beside the parts.
 */

import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import { newSplitDraft } from '@/lib/transactions/splits'
import { category } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'

import { SplitEditor } from './SplitEditor'

const noop = () => undefined

function render(amounts: string[], explain = false): string {
  return renderScreen(
    <SplitEditor
      parent={moneyFromCents(-5_000)}
      drafts={amounts.map((amount) => newSplitDraft(amount))}
      categories={[category('groceries', 'Groceries')]}
      frequentCategoryIds={[]}
      saving={false}
      explain={explain}
      onChange={noop}
    />,
  )
}

describe('the split grid inside the transaction form', () => {
  it('has no Save button of its own', () => {
    const html = render(['-30.00', '-20.00'])

    expect(html).not.toContain('Save splits')
    expect(html).toContain('Fully allocated.')
  })

  it('says why the parts do not add up, before anything has been typed', () => {
    const html = render(['-30.00', '-10.00'], true)

    expect(html).toContain('left to allocate')
    expect(html).toContain('Not all of the transaction has been allocated.')
  })

  it('stays quiet about problems until touched when nothing blocks a submit', () => {
    expect(render(['-30.00', '-10.00'])).not.toContain('Not all of the transaction')
  })
})
