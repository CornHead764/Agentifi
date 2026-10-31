import { describe, expect, it } from 'vitest'

import { formatElapsed } from '@/lib/clients/pulling'
import { renderScreen } from '@/test/renderScreen'

import { PullProgressLine } from './PullProgressLine'

const START = '2026-09-20T12:00:00Z'
const AT = Date.parse(START)

describe('formatElapsed', () => {
  it('counts seconds, then minutes with padded seconds', () => {
    expect(formatElapsed(0)).toBe('0s')
    expect(formatElapsed(59_900)).toBe('59s')
    expect(formatElapsed(65_000)).toBe('1m 05s')
    expect(formatElapsed(-5_000)).toBe('0s')
  })
})

describe('PullProgressLine', () => {
  it('shows the reported line with the time since the pull began', () => {
    const html = renderScreen(
      <PullProgressLine
        progress={{ line: 'Reading invoices: 2 of 9', started_at: START, updated_at: START }}
        fallback="Fetching orders…"
        now={AT + 125_000}
      />,
    )
    expect(html).toContain('Reading invoices: 2 of 9 (2m 05s)')
  })

  it('falls back to the generic line before anything is reported', () => {
    const html = renderScreen(<PullProgressLine progress={null} fallback="Fetching orders…" />)
    expect(html).toContain('Fetching orders…')
  })
})
