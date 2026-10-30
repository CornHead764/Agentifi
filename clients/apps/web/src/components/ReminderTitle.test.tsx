import { describe, expect, it, vi } from 'vitest'

import { SeriesEditor } from '@/components/SeriesEditor'
import type { Series } from '@/lib/clients/upcoming'
import { renderScreen } from '@/test/renderScreen'

import { ReminderSeriesEditor } from './ReminderTitle'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

describe('the recurring item popup opened from a reminder', () => {
  it('renders nothing until the series has loaded', () => {
    const html = renderScreen(<ReminderSeriesEditor seriesId="ser-power" onClose={() => {}} />)

    expect(html).not.toContain('Edit recurring item')
  })

  it('carries a header link to the full recurring item page', () => {
    const html = renderScreen(
      <SeriesEditor
        open
        onOpenChange={() => {}}
        series={{ id: 'ser-power', label: 'Power', splits: [], tag_ids: [] } as unknown as Series}
        headerActions={<a href="/upcoming/recurring?series=ser-power">Bills &amp; Income</a>}
        accounts={[]}
        categories={[]}
        tags={[]}
      />,
    )

    expect(html).toContain('Edit recurring item')
    expect(html).toContain('href="/upcoming/recurring?series=ser-power"')
  })
})
