/**
 * The split grid is part of the transaction form: it has no Save of its own, and
 * what blocks the form's Update is said beside the parts.
 */

import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@agentifi/core/money'
import { newSplitDraft } from '@/lib/transactions/splits'
import { category } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'

import { SplitEditor } from './SplitEditor'

const noop = () => undefined

function render(amounts: string[], explain = false, minParts?: number): string {
  return renderScreen(
    <SplitEditor
      parent={moneyFromCents(-5_000)}
      drafts={amounts.map((amount) => newSplitDraft(amount))}
      categories={[category('groceries', 'Groceries')]}
      frequentCategoryIds={[]}
      saving={false}
      explain={explain}
      minParts={minParts}
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

  it('lets a form that saves one part unsplit remove parts down to one', () => {
    const removable = (html: string) => html.split('Remove this part').length - 1

    expect(removable(render(['-30.00', '-20.00']))).toBe(0)
    expect(removable(render(['-30.00', '-20.00'], false, 1))).toBe(2)
    expect(removable(render(['-50.00'], true, 1))).toBe(0)
  })

  it('says nothing about allocating when one part is left', () => {
    expect(render(['-30.00'], true, 1)).not.toContain('left to allocate')
  })
})
