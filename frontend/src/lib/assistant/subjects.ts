/**
 * One subject builder per entity, so what the assistant is told about each
 * kind of row is one tested answer. Only `reads` reaches the prompt; money
 * fields exist for the person to recognize the row, never for the model to
 * reason from.
 */

import type { Money } from '@/lib/money'

import type { AskSubject } from './rowQuestion'

interface TransactionLike {
  id: string
  payee: string
  statement_name?: string
  amount: Money
  date: string
  effective_date?: string | null
  account_id?: string
  splits?: readonly unknown[]
}

/** Names are passed in because every screen drawing the row has already resolved them. */
export function transactionSubject(
  txn: TransactionLike,
  names: { account?: string | null; category?: string | null } = {},
): AskSubject {
  const reads = [`/transactions/${txn.id}`]
  if (txn.account_id) reads.push(`/accounts/${txn.account_id}`)
  return {
    kind: 'transaction',
    id: txn.id,
    // Fall back to the bank's wording when there is no cleaned payee.
    name: txn.payee.trim() === '' ? (txn.statement_name ?? '') : txn.payee,
    amount: txn.amount,
    date: txn.effective_date ?? txn.date,
    detail: [
      names.account,
      names.category,
      (txn.splits?.length ?? 0) > 1 ? `${txn.splits?.length} splits` : null,
    ],
    reads,
  }
}

export function accountSubject(account: {
  id: string
  name: string
  balance?: Money | null
  kind?: string | null
}): AskSubject {
  return {
    kind: 'account',
    id: account.id,
    name: account.name,
    amount: account.balance ?? null,
    detail: [account.kind],
    reads: [`/accounts/${account.id}`, `/accounts/${account.id}/summary`],
  }
}

/** A position has no endpoint of its own; `/holdings` lists it, found by symbol. */
export function holdingSubject(holding: {
  security_id?: string | null
  symbol: string
  name?: string | null
  market_value?: Money | null
  account_name?: string | null
}): AskSubject {
  const reads = ['/holdings']
  if (holding.security_id) reads.push(`/securities/${holding.security_id}`)
  return {
    kind: 'holding',
    id: holding.security_id ?? null,
    name: holding.symbol,
    amount: holding.market_value ?? null,
    detail: [holding.name, holding.account_name],
    reads,
  }
}

export function seriesSubject(series: {
  id: string
  label?: string
  display_name?: string | null
  description?: string
  expected_amount?: Money | null
  next_due_on?: string | null
}): AskSubject {
  return {
    kind: 'series',
    id: series.id,
    // Trap 7: `description` is matching input, not a label.
    name: series.label ?? series.display_name ?? series.description ?? '',
    amount: series.expected_amount ?? null,
    date: series.next_due_on ?? null,
    reads: [`/series/${series.id}`, `/series/${series.id}/history`],
  }
}

export function ruleSubject(rule: { id: string; name: string }): AskSubject {
  return {
    kind: 'rule',
    id: rule.id,
    name: rule.name,
    reads: [`/rules/${rule.id}`, `/rules/${rule.id}/preview`],
  }
}

export function guidanceSubject(note: { id: string; name: string }): AskSubject {
  return {
    kind: 'guidance',
    id: note.id,
    name: note.name,
    reads: [`/guidance/${note.id}`],
  }
}

export function goalSubject(goal: {
  id: string
  name: string
  target_amount?: Money | null
  target_date?: string | null
}): AskSubject {
  return {
    kind: 'goal',
    id: goal.id,
    name: goal.name,
    amount: goal.target_amount ?? null,
    date: goal.target_date ?? null,
    reads: ['/goals'],
  }
}

export function watchlistSubject(watchlist: { id: string; name: string }): AskSubject {
  return {
    kind: 'watchlist',
    id: watchlist.id,
    name: watchlist.name,
    reads: [`/watchlists/${watchlist.id}`],
  }
}

/** The plan is materialized per month, so the month is part of the reference. */
export function bucketSubject(bucket: {
  name: string
  month: string
  amount?: Money | null
  id?: string | null
}): AskSubject {
  return {
    kind: 'bucket',
    id: bucket.id ?? null,
    name: bucket.name,
    amount: bucket.amount ?? null,
    detail: [monthName(bucket.month)],
    reads: [`/spending-plan/${bucket.month}`],
  }
}

/** "2026-09" and "2026-09-01" both name September. */
function monthName(month: string): string {
  return month.slice(0, 7)
}

export function billSubject(occurrence: {
  id?: string | null
  name: string
  amount?: Money | null
  due_on: string
  series_id?: string | null
}): AskSubject {
  const reads = ['/occurrences']
  if (occurrence.series_id) reads.push(`/series/${occurrence.series_id}`)
  return {
    kind: 'bill',
    id: occurrence.id ?? null,
    name: occurrence.name,
    amount: occurrence.amount ?? null,
    date: occurrence.due_on,
    reads,
  }
}

export function orderSubject(order: {
  id: string
  merchant: string
  title: string
  total?: Money | null
  ordered_on?: string | null
}): AskSubject {
  return {
    kind: 'order',
    id: order.id,
    name: order.title,
    amount: order.total ?? null,
    date: order.ordered_on ?? null,
    detail: [order.merchant],
    reads: [`/merchants/${order.merchant}/orders`],
  }
}

export function categorySubject(category: { id: string; name: string }): AskSubject {
  return {
    kind: 'category',
    id: category.id,
    name: category.name,
    reads: [`/categories/${category.id}`],
  }
}

export function tagSubject(tag: { id: string; name: string }): AskSubject {
  return {
    kind: 'tag',
    id: tag.id,
    name: tag.name,
    reads: [`/tags/${tag.id}`],
  }
}

/** The whole net worth screen, which one request backs. */
export function netWorthSubject(): AskSubject {
  return { kind: 'networth', name: 'Net worth', reads: ['/net-worth', '/accounts'] }
}

/** Every position; a single holding is `holdingSubject`. */
export function portfolioSubject(): AskSubject {
  return { kind: 'portfolio', name: 'Investment portfolio', reads: ['/holdings'] }
}
