/**
 * What the account's cash-flow card shows: the projection, the history, or both
 * on one axis. On the shared day each line keeps its own figure; neither is
 * patched to meet the other.
 */

import type { IsoDate } from '@/lib/clients/entities'
import { readStoredChoice, writeStored } from '@/lib/storage'

export const CASH_FLOW_VIEWS = ['projected', 'historical', 'both'] as const
export type CashFlowView = (typeof CASH_FLOW_VIEWS)[number]

export const CASH_FLOW_VIEW_LABELS: Record<CashFlowView, string> = {
  projected: 'Projected cash flow',
  historical: 'Historical cash flow',
  both: 'Historical and projected',
}

export const HISTORY_RANGES = [30, 60, 90, 180, 365] as const
export type HistoryRange = (typeof HISTORY_RANGES)[number]
export const DEFAULT_HISTORY_RANGE: HistoryRange = 90

export interface CashFlowPrefs {
  view: CashFlowView
  historyDays: HistoryRange
}

/** Per signed-in user: two people may share a browser. */
function prefKey(userId: string | null | undefined, field: string): string {
  return `cash-flow.${userId ?? 'anonymous'}.${field}`
}

export function readCashFlowPrefs(userId: string | null | undefined): CashFlowPrefs {
  const view = readStoredChoice(prefKey(userId, 'view'), CASH_FLOW_VIEWS, 'projected')
  const days = readStoredChoice(
    prefKey(userId, 'history-days'),
    HISTORY_RANGES.map(String),
    String(DEFAULT_HISTORY_RANGE),
  )
  const historyDays =
    HISTORY_RANGES.find((range) => String(range) === days) ?? DEFAULT_HISTORY_RANGE
  return { view, historyDays }
}

export function writeCashFlowPrefs(userId: string | null | undefined, prefs: CashFlowPrefs): void {
  writeStored(prefKey(userId, 'view'), prefs.view)
  writeStored(prefKey(userId, 'history-days'), String(prefs.historyDays))
}

export interface DatedBalance {
  on: IsoDate
  balance: number
}

export interface JoinedCashFlow {
  /** Oldest first. */
  axis: IsoDate[]
  history: Record<IsoDate, number | null>
  projection: Record<IsoDate, number | null>
  /** The day the two meet, when both are drawn; null otherwise. */
  today: IsoDate | null
}

/** History is cut at `today` and the projection starts there, so clock skew cannot draw either on the other's side. */
export function joinCashFlow(
  history: readonly DatedBalance[],
  projection: readonly DatedBalance[],
  today: IsoDate,
): JoinedCashFlow {
  const past = history.filter((point) => point.on <= today)
  const ahead = projection.filter((point) => point.on >= today)
  const days = new Set<IsoDate>([...past.map((p) => p.on), ...ahead.map((p) => p.on)])
  const axis = [...days].sort()
  const historyByDay: Record<IsoDate, number | null> = {}
  const projectionByDay: Record<IsoDate, number | null> = {}
  for (const day of axis) {
    historyByDay[day] = null
    projectionByDay[day] = null
  }
  for (const point of past) historyByDay[point.on] = point.balance
  for (const point of ahead) projectionByDay[point.on] = point.balance
  const meets = past.length > 0 && ahead.length > 0 && days.has(today)
  return { axis, history: historyByDay, projection: projectionByDay, today: meets ? today : null }
}
