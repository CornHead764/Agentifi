import { afterEach, describe, expect, it } from 'vitest'

import {
  DEFAULT_HISTORY_RANGE,
  joinCashFlow,
  readCashFlowPrefs,
  writeCashFlowPrefs,
} from './cashFlowView'

function installStorage(options: { throws?: boolean } = {}): Map<string, string> {
  const backing = new Map<string, string>()
  const storage = {
    getItem: (key: string) => {
      if (options.throws) throw new Error('storage is disabled')
      return backing.get(key) ?? null
    },
    setItem: (key: string, value: string) => {
      if (options.throws) throw new Error('quota exceeded')
      backing.set(key, value)
    },
    removeItem: (key: string) => void backing.delete(key),
  }
  Reflect.set(globalThis, 'window', { localStorage: storage })
  return backing
}

afterEach(() => {
  Reflect.deleteProperty(globalThis, 'window')
})

describe('the card remembers how each person reads it', () => {
  it('starts on the projection with ninety days of history', () => {
    installStorage()
    expect(readCashFlowPrefs('user-a')).toEqual({
      view: 'projected',
      historyDays: DEFAULT_HISTORY_RANGE,
    })
  })

  it('keeps one person’s choice apart from another’s', () => {
    installStorage()
    writeCashFlowPrefs('user-a', { view: 'both', historyDays: 30 })
    expect(readCashFlowPrefs('user-a')).toEqual({ view: 'both', historyDays: 30 })
    expect(readCashFlowPrefs('user-b').view).toBe('projected')
  })

  it('falls back on a value an older build or a hand edit left behind', () => {
    const backing = installStorage()
    backing.set('agentifi.cash-flow.user-a.view', 'sideways')
    backing.set('agentifi.cash-flow.user-a.history-days', '45')
    expect(readCashFlowPrefs('user-a')).toEqual({
      view: 'projected',
      historyDays: DEFAULT_HISTORY_RANGE,
    })
  })

  it('never throws when the browser refuses storage', () => {
    installStorage({ throws: true })
    expect(() => writeCashFlowPrefs('user-a', { view: 'historical', historyDays: 60 })).not.toThrow()
    expect(readCashFlowPrefs('user-a').view).toBe('projected')
  })

  it('works with no window at all', () => {
    expect(readCashFlowPrefs('user-a').view).toBe('projected')
  })
})

describe('joinCashFlow', () => {
  const history = [
    { on: '2026-09-24', balance: 900 },
    { on: '2026-09-25', balance: 850 },
    { on: '2026-09-26', balance: 800 },
  ]
  const projection = [
    { on: '2026-09-26', balance: 800 },
    { on: '2026-09-27', balance: 700 },
  ]

  it('puts both lines on one axis and meets them on today', () => {
    const joined = joinCashFlow(history, projection, '2026-09-26')
    expect(joined.axis).toEqual(['2026-09-24', '2026-09-25', '2026-09-26', '2026-09-27'])
    expect(joined.history).toEqual({
      '2026-09-24': 900,
      '2026-09-25': 850,
      '2026-09-26': 800,
      '2026-09-27': null,
    })
    expect(joined.projection).toEqual({
      '2026-09-24': null,
      '2026-09-25': null,
      '2026-09-26': 800,
      '2026-09-27': 700,
    })
    expect(joined.today).toBe('2026-09-26')
  })

  it('keeps each line on its own side of today', () => {
    const joined = joinCashFlow(
      [...history, { on: '2026-09-27', balance: 1 }],
      [{ on: '2026-09-25', balance: 2 }, ...projection],
      '2026-09-26',
    )
    expect(joined.history['2026-09-27']).toBeNull()
    expect(joined.projection['2026-09-25']).toBeNull()
  })

  it('does not mark a meeting when one line is missing', () => {
    expect(joinCashFlow(history, [], '2026-09-26').today).toBeNull()
    expect(joinCashFlow([], projection, '2026-09-26').today).toBeNull()
  })

  it('keeps each line’s own figure on the day they share', () => {
    const joined = joinCashFlow(history, [{ on: '2026-09-26', balance: 780 }], '2026-09-26')
    expect(joined.history['2026-09-26']).toBe(800)
    expect(joined.projection['2026-09-26']).toBe(780)
  })
})
