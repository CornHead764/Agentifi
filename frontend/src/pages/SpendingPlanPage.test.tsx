/**
 * The spending-plan screen against a seeded cache: the rail resolves the
 * buckets to the engine's headline, the default bucket's rows are on screen,
 * and a closed-out month says so instead of silently refusing edits.
 */

import { QueryClient } from '@tanstack/react-query'
import { Route, Routes } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import { planMonth } from '@/components/plan/__fixtures__/plan'
import { ApiError } from '@/lib/api'
import { CURRENT_SPACE_KEY, type Space } from '@/lib/clients/spaces'
import { type SpendingPlanMonth } from '@/lib/spendingPlan'
import { renderScreen } from '@/test/renderScreen'
import { SpendingPlanPage } from './SpendingPlanPage'
import { monthKey as thisMonth } from '@/lib/format'

const SPACE: Space = {
  id: 'space-1',
  name: 'Household',
  primary_currency: 'USD',
  timezone: 'UTC',
  default_date_range: '',
  sidebar_account_types: null,
  role: 'owner',
  can_write: true,
  is_owner: true,
  joined_at: '2026-01-01T00:00:00Z',
}

function render(
  month: SpendingPlanMonth,
  path = '/spending-plan',
  monthKey = thisMonth(),
  defaultRange = '',
): string {
  return renderScreen(
    <Routes>
      <Route path="/spending-plan" element={<SpendingPlanPage />} />
      <Route path="/spending-plan/:bucket" element={<SpendingPlanPage />} />
    </Routes>,
    {
      route: path,
      seed: [
        [['spending-plan', monthKey], month],
        [['categories', 'expense'], []],
        [CURRENT_SPACE_KEY, { ...SPACE, default_date_range: defaultRange }],
      ],
    },
  )
}

/**
 * The same page with the month query failed. `retryOnMount: false` keeps the
 * seeded failure: react-query otherwise resets an errored query with no data
 * to pending when an observer mounts.
 */
function renderFailure(error: Error): string {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, retryOnMount: false } },
  })
  client.setQueryData(['categories', 'expense'], [])
  const key = ['spending-plan', thisMonth()]
  const query = client.getQueryCache().build(client, client.defaultQueryOptions({ queryKey: key }))
  query.setState({ status: 'error', error, fetchStatus: 'idle', errorUpdatedAt: Date.now() })
  return renderScreen(
    <Routes>
      <Route path="/spending-plan" element={<SpendingPlanPage />} />
    </Routes>,
    { route: '/spending-plan', client },
  )
}

describe('the spending plan screen', () => {
  it('explains a failed load without quoting the status or the path', () => {
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {})
    const rendered = renderFailure(new ApiError(500, '/spending-plan/2026-09', null, 'req-4'))
    expect(rendered).toContain('The server had a problem')
    expect(rendered).toContain('req-4')
    expect(rendered).not.toContain('/spending-plan/2026-09')
    expect(logged.mock.calls[0][0]).toContain('/spending-plan/2026-09')
    logged.mockRestore()
  })

  it('stacks the buckets and prints the engine headline', () => {
    const rendered = render(planMonth())
    for (const label of ['Income', 'Bills', 'Planned Spend', 'Other Spend', 'Goals']) {
      expect(rendered).toContain(label)
    }
    expect(rendered).toContain('1,100.00')
  })

  it('opens on Income with its rows and its excluded disclosure', () => {
    const rendered = render(planMonth())
    expect(rendered).toContain('Acme Corp Payroll')
    expect(rendered).toContain('Received')
    expect(rendered).toContain('Excluded this month (1)')
  })

  it('routes each bucket, the way Simplifi does', () => {
    // /spending-plan/bills opens the Bills panel; the URL is the selection.
    const rendered = render(planMonth(), '/spending-plan/bills')
    expect(rendered).toContain('No bills this month')
    expect(rendered).toContain('3,200.00')
  })

  it('reads the month from the date parameter', () => {
    // ?date=2026-05-01 asks for May: the page queries that month's cache, not
    // the current one.
    const rendered = render(planMonth(), '/spending-plan?date=2026-05-01', '2026-05')
    expect(rendered).toContain('1,100.00')
  })

    /**
     * The space's "default date range" does not move this page. Every chip it
     * offers ends today, so the only month it could name is the current one,
     * and a materialized month is one row, not a window.
     */
  it('opens on the current month whatever the space default range says', () => {
    const rendered = render(planMonth(), '/spending-plan', thisMonth(), '6M')
    expect(rendered).toContain('1,100.00')
    expect(rendered).toContain('Income')
  })

  it('offers the way back to the current month only from another month', () => {
    expect(render(planMonth())).not.toContain('This month<')
    expect(render(planMonth(), '/spending-plan?date=2026-05-01', '2026-05')).toContain('This month<')
  })

  it('says a closed-out month is frozen instead of silently refusing edits', () => {
    const rendered = render(planMonth({ is_closed_out: true, closed_out_at: '2026-09-01' }))
    expect(rendered.toLowerCase()).toContain('closed out')
  })
})
