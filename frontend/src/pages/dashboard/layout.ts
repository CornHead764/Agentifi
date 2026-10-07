import { useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'

import { api } from '@/lib/api'
import type { Uuid } from '@/lib/transactions/types'
import { CURRENT_SPACE_KEY } from '@/lib/clients/spaces'
import { readStoredJson, writeStoredJson } from '@/lib/storage'

/**
 * The dashboard layout: which widgets are on, and in what order. Stored on the
 * membership and mirrored into this browser, since the page renders before the
 * query resolves.
 *
 * Reading is defensive: the stored value can be hand-edited or left by an
 * older build, so an unknown widget id is dropped, a missing one is appended in
 * its default position, and anything unparseable falls back to the defaults.
 *
 * An entry can also carry the accounts its widget reads (Recent
 * Transactions): strings survive, everything else is dropped, and an entry
 * that never named any has no key rather than an empty list.
 * `lib/accountScope` is what an absent list means.
 */

const STORAGE_KEY = 'dashboardLayout'

export const WIDGET_IDS = [
  'recent',
  'review',
  'net_worth',
  'bills',
  'income',
  'spending',
  'savings_rate',
  'top_categories',
  'spending_plan',
  'planned_spend',
  'goals',
  'watchlist',
  'holdings',
  'investments',
] as const

export type WidgetId = (typeof WIDGET_IDS)[number]

export const WIDGET_TITLES: Record<WidgetId, string> = {
  recent: 'Recent Transactions',
  review: 'Review Transactions',
  net_worth: 'Net Worth',
  bills: 'Bills & Income',
  income: 'Income',
  spending: 'Spending',
  savings_rate: 'Savings Rate',
  top_categories: 'Top Spending Categories',
  spending_plan: 'Spending Plan',
  planned_spend: 'Planned Spend',
  goals: 'Savings Goals',
  watchlist: 'Watchlists',
  holdings: 'Holdings',
  investments: 'Investments',
}

/** How many grid columns each widget claims at full width. */
export const WIDGET_SPANS: Record<WidgetId, 1 | 2> = {
  recent: 1,
  review: 1,
  net_worth: 2,
  bills: 1,
  income: 1,
  spending: 1,
  savings_rate: 1,
  top_categories: 1,
  spending_plan: 1,
  planned_spend: 1,
  goals: 2,
  watchlist: 2,
  holdings: 1,
  investments: 1,
}

export interface WidgetLayout {
  id: WidgetId
  on: boolean
  /**
   * Which accounts this widget reads. Absent, never an empty array, is "nobody
   * has chosen" (`lib/accountScope`); an empty array is a person who unticked
   * every account and should see nothing.
   */
  accounts?: Uuid[]
}

/**
 * What a dashboard opens with: the daily glance, in the order the questions
 * get asked. The rest are a tick away under Customize; a stored layout
 * outranks this.
 */
const DEFAULT_ON: readonly WidgetId[] = [
  'review',
  'bills',
  'spending_plan',
  'recent',
  'net_worth',
]

export const DEFAULT_LAYOUT: readonly WidgetLayout[] = [
  ...DEFAULT_ON.map((id) => ({ id, on: true })),
  ...WIDGET_IDS.filter((id) => !DEFAULT_ON.includes(id)).map((id) => ({ id, on: false })),
]

function isWidgetId(value: unknown): value is WidgetId {
  return typeof value === 'string' && WIDGET_IDS.some((id) => id === value)
}

/**
 * An account selection off the stored JSON, or undefined for "never chosen".
 * Ids are not checked against the account list here: a stale one is dropped
 * where the widget reads it, since dropping it here would rewrite the choice
 * on the next save.
 */
function parseAccounts(value: unknown): Uuid[] | undefined {
  if (!Array.isArray(value)) return undefined
  return value.filter((id): id is Uuid => typeof id === 'string')
}

/** Validated, never trusted. Unknown ids drop out; missing ones are appended. */
export function parseLayout(raw: unknown): WidgetLayout[] {
  if (!Array.isArray(raw)) return [...DEFAULT_LAYOUT]

  const seen = new Set<WidgetId>()
  const layout: WidgetLayout[] = []

  for (const entry of raw) {
    if (typeof entry !== 'object' || entry === null) continue
    const record: Record<string, unknown> = { ...entry }
    const id = record.id
    if (!isWidgetId(id) || seen.has(id)) continue
    seen.add(id)
    const accounts = parseAccounts(record.accounts)
    layout.push(accounts === undefined ? { id, on: record.on !== false } : { id, on: record.on !== false, accounts })
  }

  for (const fallback of DEFAULT_LAYOUT) {
    if (!seen.has(fallback.id)) layout.push({ ...fallback })
  }

  return layout
}

/**
 * Point a widget at a set of accounts, or hand it back to the default rule.
 * `null` removes the key rather than storing an empty array, which means none.
 */
export function setWidgetAccounts(
  layout: readonly WidgetLayout[],
  id: WidgetId,
  accounts: readonly Uuid[] | null,
): WidgetLayout[] {
  return layout.map((entry) => {
    if (entry.id !== id) return entry
    if (accounts === null) {
      const rest: WidgetLayout = { ...entry }
      delete rest.accounts
      return rest
    }
    return { ...entry, accounts: [...accounts] }
  })
}

function readLayout(): WidgetLayout[] {
  return parseLayout(readStoredJson(STORAGE_KEY))
}

function writeLayout(layout: readonly WidgetLayout[]): void {
  writeStoredJson(STORAGE_KEY, layout)
}

/**
 * The arrangement from the server, falling back to this browser's. The local
 * copy is written on every change and is what the first paint reads.
 */
export function useDashboardLayout(): {
  layout: WidgetLayout[]
  apply: (next: WidgetLayout[]) => void
} {
  // What this session has changed outranks both: the server's older answer
  // must not arrive and undo a move just made.
  const [changed, setChanged] = useState<WidgetLayout[] | null>(null)
  // Read once, at mount, so the dashboard does not rearrange itself a moment
  // after loading.
  const [local] = useState(readLayout)

  const stored = useQuery({
    queryKey: [...CURRENT_SPACE_KEY, 'dashboard'],
    queryFn: ({ signal }) =>
      api.get<{ layout: unknown }>('/spaces/current/dashboard', undefined, signal),
  })

  // Null is "never arranged", which is this browser's copy or the built-in
  // order — not an instruction to reset what is on screen.
  const fromServer = useMemo(() => {
    const raw = stored.data?.layout
    return raw === undefined || raw === null ? null : parseLayout(raw)
  }, [stored.data])

  const apply = (next: WidgetLayout[]) => {
    setChanged(next)
    writeLayout(next)
    // Fire and forget: a layout that will not save is not worth interrupting
    // the session for, and the local copy has it either way.
    void api.put('/spaces/current/dashboard', { layout: next }).catch(() => undefined)
  }

  return { layout: changed ?? fromServer ?? local, apply }
}

export function moveWidget(
  layout: readonly WidgetLayout[],
  id: WidgetId,
  direction: -1 | 1,
): WidgetLayout[] {
  const index = layout.findIndex((entry) => entry.id === id)
  const target = index + direction
  if (index < 0 || target < 0 || target >= layout.length) return [...layout]

  const next = [...layout]
  const [moved] = next.splice(index, 1)
  next.splice(target, 0, moved)
  return next
}

export function toggleWidget(layout: readonly WidgetLayout[], id: WidgetId): WidgetLayout[] {
  return layout.map((entry) => (entry.id === id ? { ...entry, on: !entry.on } : entry))
}
