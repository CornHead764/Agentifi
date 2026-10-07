import { describe, expect, it } from 'vitest'

import type { SuggestionBatch, SuggestionTally } from '@/lib/clients/suggestionBatches'
import { renderScreen } from '@/test/renderScreen'

import { CategoryCheckStrip } from './CategoryCheckStrip'

const NONE: SuggestionTally = {
  agreed: 0,
  differs: 0,
  unsure: 0,
  suggested: 0,
  undetermined: 0,
  skipped: 0,
  failed: 0,
}

function strip(batch: Partial<SuggestionBatch>): string {
  const whole: SuggestionBatch = {
    id: 'b1',
    created_at: '2026-09-30T10:00:00Z',
    cancelled: false,
    dismissed: false,
    rows: 0,
    done: 0,
    pending: 0,
    not_run: 0,
    finished: true,
    reviewed: NONE,
    unreviewed: NONE,
    ...batch,
  }
  return renderScreen(
    <CategoryCheckStrip
      batch={whole}
      onShowUndetermined={() => {}}
      onCancel={() => {}}
      cancelling={false}
      onDismiss={() => {}}
    />,
  )
}

describe('CategoryCheckStrip', () => {
  it('counts a running request off and offers to cancel it', () => {
    const html = strip({ rows: 640, done: 211, pending: 429, finished: false })
    expect(html).toContain('Suggesting categories — 211 of 640')
    expect(html).toContain('>Cancel<')
  })

  it('compares a finished request with the categories the rows had', () => {
    const html = strip({
      rows: 12,
      done: 12,
      reviewed: { ...NONE, agreed: 5, differs: 1 },
      unreviewed: { ...NONE, agreed: 3, undetermined: 3 },
    })
    expect(html).toContain('Suggestions done for 12 rows')
    expect(html).toContain('Agreed with 8 of 9 categories already set: 5 of 6 reviewed, 3 of 3 unreviewed.')
    expect(html).toContain('3 undetermined')
    expect(html).not.toContain('>Cancel<')
  })
})
