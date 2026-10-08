/**
 * Net Worth: the headline, group rows and chart come from one response so
 * they cannot pair differently resolved windows. A group row's change is over
 * the selected window, not month over month.
 */

import { useQuery } from '@tanstack/react-query'

import {
  GetNetWorthRequestSchema,
  GetNetWorthResponseSchema,
  NetWorthService,
} from '@/gen/agentifi/v1/net_worth_pb'
import { fromWire, toWire } from '@/lib/rpc/wire'
import { rpcClient, unary } from '@/lib/rpcSession'
import type { AccountKind } from '@/lib/transactions/types'
import { ZERO_MONEY, type Money } from '@/lib/money'

import { windowFor, type RangePreset } from '@/lib/dateRanges'

import type { EchoedWindow, IsoDate, WireRate } from './entities'

export const netWorthKeys = {
  all: ['net-worth'] as const,
  window: (from: string | null, to: string) => ['net-worth', from, to] as const,
}

/** Debt kinds are positive, like `debt`. */
export interface NetWorthKindAmount {
  kind: AccountKind
  amount: Money
}

export interface NetWorthPoint {
  on: IsoDate
  assets: Money
  /** Positive, even though a debt balance is stored negative. */
  debt: Money
  net: Money
  /** Every point carries the same kinds in the same order. */
  by_kind: NetWorthKindAmount[]
  /** Assets less the loans secured on them (`domain.EquityAt`). */
  equity: Money
}

export interface NetWorthAccount {
  account_id: string
  name: string
  kind: AccountKind
  /** A picker label ("roth_ira"); never enters a calculation. */
  type: string
  is_closed: boolean
  start: Money
  end: Money
  change: Money
  /** Null when the window opened at zero. */
  change_pct: WireRate
}

/** `kind` drives the arithmetic, `class` is the picker's label, `side` the panel half. */
export interface NetWorthGroup {
  kind: AccountKind
  class: string
  side: 'asset' | 'debt'
  account_count: number
  start: Money
  end: Money
  change: Money
  change_pct: WireRate
  accounts: NetWorthAccount[]
}

export interface NetWorth {
  window: EchoedWindow
  /** The server coarsens rather than truncating. */
  granularity: string
  points: NetWorthPoint[]
  start: NetWorthPoint
  end: NetWorthPoint
  change: Money
  change_pct: WireRate
  /** At the window's end; null when there are no assets. */
  debt_to_asset: WireRate
  groups: NetWorthGroup[]
  included_accounts: number
  total_accounts: number
  /** Counted at face value for want of a rate, so the total is approximate. */
  unconverted_currencies: string[]
}

const netWorth = rpcClient(NetWorthService)

/** Explicit ends, so a page pairing this with another ranged read sends both the same window. */
export function useNetWorthWindow(from: IsoDate | null, to: IsoDate) {
  return useQuery({
    queryKey: netWorthKeys.window(from, to),
    queryFn: ({ signal }) =>
      unary(NetWorthService.method.getNetWorth, async () => {
        const answer = await netWorth.getNetWorth(toWire(GetNetWorthRequestSchema, { from, to }), { signal })
        return fromWire<NetWorth>(GetNetWorthResponseSchema, answer)
      }),
  })
}

export function useNetWorth(range: RangePreset) {
  const bounds = windowFor(range)
  return useNetWorthWindow(bounds.from, bounds.to)
}

export function groupsOn(net: NetWorth, side: 'asset' | 'debt'): NetWorthGroup[] {
  return net.groups.filter((group) => group.side === side)
}

const KIND_LABELS: Record<AccountKind, string> = {
  cash: 'Cash & Checking',
  investment: 'Investments',
  asset: 'Assets',
  credit_card: 'Credit Cards',
  loan: 'Loans',
}

export function kindLabel(kind: AccountKind): string {
  return KIND_LABELS[kind]
}

export function groupLabel(group: NetWorthGroup): string {
  return kindLabel(group.kind)
}

/** Mirrors `domain.AccountKind.IsDebt`, which decides a group row's `side`. */
const DEBT_KINDS: ReadonlySet<AccountKind> = new Set<AccountKind>(['credit_card', 'loan'])

function kindSide(kind: AccountKind): 'asset' | 'debt' {
  return DEBT_KINDS.has(kind) ? 'debt' : 'asset'
}

/** Read off the points, not the groups, which describe only the window's end. */
export function kindsOn(
  points: readonly NetWorthPoint[],
  side: 'asset' | 'debt',
): AccountKind[] {
  return (points[0]?.by_kind ?? [])
    .filter((entry) => kindSide(entry.kind) === side)
    .map((entry) => entry.kind)
}

/** Absent is zero. */
export function amountOn(point: NetWorthPoint, kind: AccountKind): Money {
  return point.by_kind.find((entry) => entry.kind === kind)?.amount ?? ZERO_MONEY
}
