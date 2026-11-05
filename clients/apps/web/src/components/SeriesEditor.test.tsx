import { describe, expect, it, vi } from 'vitest'

import { SeriesEditor } from '@/components/SeriesEditor'
import type { Series } from '@/lib/clients/upcoming'
import { parseMoney } from '@agentifi/core/money'
import { renderScreen } from '@/test/renderScreen'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

describe('the series editor’s split', () => {
  it('says which linked transactions take it (calculations.md §9)', () => {
    const series = {
      id: 'ser-water',
      label: 'Water bill',
      amount: parseMoney('-100.00'),
      tag_ids: [],
      splits: [
        { amount: parseMoney('-60.00'), category_id: null, memo: 'water', tag_ids: [] },
        { amount: parseMoney('-40.00'), category_id: null, memo: 'sewer', tag_ids: [] },
      ],
    } as unknown as Series
    const html = renderScreen(
      <SeriesEditor
        open
        onOpenChange={() => {}}
        series={series}
        accounts={[]}
        categories={[]}
        tags={[]}
      />,
    )

    expect(html).toContain('A linked transaction with no category or split of its own is split this way')
    expect(html).not.toContain('A different bill amount is split')
  })
})
