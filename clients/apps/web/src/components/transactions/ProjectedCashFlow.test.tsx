/** The account's cash-flow card draws the view its reader last chose. */

import { afterEach, describe, expect, it } from 'vitest'

import { balanceHistoryKeys } from '@/lib/clients/balancehistory'
import { cashFlowForecastKeys } from '@/lib/clients/cashflowforecast'
import { cashFlowKeys } from '@/lib/clients/upcoming/keys'
import { toIsoDate } from '@/lib/format'
import { moneyFromCents } from '@/lib/money'
import { dayWindow } from '@/lib/dateRanges'
import type { Account } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { ProjectedCashFlow } from './ProjectedCashFlow'

const ACCOUNT = { id: 'a1', name: 'Everyday Checking' } as unknown as Account

function installView(view: string): void {
  const backing = new Map<string, string>([['agentifi.cash-flow.anonymous.view', view]])
  Reflect.set(globalThis, 'window', {
    localStorage: {
      getItem: (key: string) => backing.get(key) ?? null,
      setItem: (key: string, value: string) => void backing.set(key, value),
      removeItem: (key: string) => void backing.delete(key),
    },
  })
}

afterEach(() => {
  Reflect.deleteProperty(globalThis, 'window')
})

function render(forecast: Record<string, unknown> = { available: false }): string {
  const today = new Date()
  const ahead = new Date(today)
  ahead.setDate(ahead.getDate() + 90)
  const from = toIsoDate(today)
  const to = toIsoDate(ahead)
  const flow = {
    window: { from, to, date_field: 'date' },
    threshold: moneyFromCents(0),
    accounts: [
      {
        account_id: 'a1',
        name: 'Everyday Checking',
        starting_balance: moneyFromCents(50_000),
        points: [
          { on: from, balance: moneyFromCents(50_000) },
          { on: to, balance: moneyFromCents(40_000) },
        ],
        lowest: null,
        first_below: null,
      },
    ],
    combined: [],
    occurrences: [],
  }
  const past = dayWindow(90, 0)
  const history = {
    account_id: 'a1',
    from: past.from,
    to: past.to,
    balance: moneyFromCents(50_000),
    points: [
      { on: past.from, balance: moneyFromCents(60_000) },
      { on: past.to, balance: moneyFromCents(50_000) },
    ],
  }
  return renderScreen(<ProjectedCashFlow account={ACCOUNT} />, {
    seed: [
      [cashFlowKeys.range(from, to, 'a1', '0'), flow],
      [balanceHistoryKeys.range('a1', past.from, past.to), history],
      [cashFlowForecastKeys.forecast(['a1'], '30,60,90,180'), forecast],
    ],
  })
}

describe('the account cash-flow card', () => {
  it('opens on the projection, with the options menu that re-runs it', () => {
    installView('projected')
    const markup = render()
    expect(markup).toContain('Projected cash flow')
    expect(markup).toContain('Next 90 days')
    expect(markup).toContain('aria-label="Actions for the cash flow"')
  })

  it('shows the history over the remembered range', () => {
    installView('historical')
    const markup = render()
    expect(markup).toContain('Historical cash flow')
    expect(markup).toContain('Last 90 days')
    expect(markup).not.toContain('Estimated cash flow')
  })

  it('draws both lines, the projection dashed, on one chart', () => {
    installView('both')
    const markup = render()
    expect(markup).toContain('Historical and projected')
    expect(markup).toContain('90 back · 90 ahead')
    expect(markup).toContain('Actual')
    expect(markup).toContain('chart-legend__swatch--dashed')
  })

  it('labels an account’s own estimate as an average', () => {
    installView('projected')
    const today = toIsoDate(new Date())
    const markup = render({
      available: true,
      method: 'average',
      age_days: 0,
      narrative: 'This account’s average month.',
      windows: [
        {
          days: 90,
          through: today,
          money_in: moneyFromCents(0),
          money_out: moneyFromCents(10_000),
          net: moneyFromCents(-10_000),
          covered_days: 91,
          is_complete: true,
          estimated_balance: null,
          scheduled_balance: null,
          difference: null,
        },
      ],
    })
    expect(markup).toContain('Averaged from this account’s history, today')
    expect(markup).toContain('Net, next 90 days')
  })
})
