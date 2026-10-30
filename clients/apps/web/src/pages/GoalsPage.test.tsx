/**
 * The goal card is drawn by the goal's stage: what is left to save while
 * saving, and once funded, where the money went. Figures are invented.
 */

import { describe, expect, it } from 'vitest'

import type { Goal } from '@/lib/goals'
import { moneyFromCents } from '@/lib/money'
import { renderScreen } from '@/test/renderScreen'

import { GoalCard } from './GoalsPage'

function goal(overrides: Partial<Goal> = {}): Goal {
  return {
    id: 'g1',
    name: 'Lake Trip',
    emoji: null,
    account_id: 'savings',
    account_name: 'Rainy Day Savings',
    funding_account_ids: ['savings'],
    funding: [],
    target_amount: moneyFromCents(100_000),
    target_on: '2026-12-31',
    completed_on: null,
    closed_on: null,
    stage: 'saving',
    is_taken_from_plan: true,
    saved_so_far: moneyFromCents(40_000),
    withdrawn: moneyFromCents(0),
    spent_on_goal: moneyFromCents(0),
    spending_by_category: [],
    unassigned_withdrawn: moneyFromCents(0),
    funded: moneyFromCents(40_000),
    contributed_this_month: moneyFromCents(0),
    left_to_save: moneyFromCents(60_000),
    monthly_needed: moneyFromCents(12_000),
    months_to_target: 5,
    target_has_passed: false,
    pct_complete: '40',
    pct_funded: '40',
    is_complete: false,
    is_funded: false,
    txn_ids: [],
    withdrawal_txn_ids: [],
    spending_txn_ids: [],
    contributions: [],
    ...overrides,
  }
}

/** Saved 1,000.00 in full, took 600.00 out, linked 450.00 of purchases. */
const SPENDING = goal({
  stage: 'spending',
  saved_so_far: moneyFromCents(40_000),
  withdrawn: moneyFromCents(60_000),
  spent_on_goal: moneyFromCents(45_000),
  unassigned_withdrawn: moneyFromCents(15_000),
  funded: moneyFromCents(100_000),
  left_to_save: moneyFromCents(60_000),
  monthly_needed: null,
  pct_complete: '40',
  pct_funded: '100',
  is_funded: true,
  target_has_passed: true,
})

function card(one: Goal): string {
  return renderScreen(
    <GoalCard goal={one} onEdit={() => {}} onDelete={() => {}} onClose={() => {}} onMove={() => {}} />,
  )
}

describe('the goal card', () => {
  it('asks for what is left while saving', () => {
    const html = card(goal())

    expect(html).toContain('Saving')
    expect(html).toContain('Left to save')
    expect(html).toContain('600.00')
    expect(html).toContain('Per month')
    expect(html).not.toContain('Taken out')
  })

  it('shows where the money went once it is being spent, and never what is left to save', () => {
    const html = card(SPENDING)

    expect(html).toContain('Spending')
    expect(html).toContain('Taken out')
    expect(html).toContain('Not yet linked to spending')
    expect(html).toContain('150.00')
    expect(html).toContain('Still in Rainy Day Savings')
    expect(html).toContain('Find rows')
    expect(html).not.toContain('Left to save')
    expect(html).not.toContain('to save')
    expect(html).not.toContain('passed')
    expect(html).not.toContain('Goal completed')
  })

  it('says nothing is unlinked when the purchases account for every withdrawal', () => {
    const html = card({ ...SPENDING, unassigned_withdrawn: moneyFromCents(0) })

    expect(html).not.toContain('Not yet linked to spending')
  })

  it('tells a funded goal how to record the spending', () => {
    const html = card(
      goal({
        stage: 'funded',
        saved_so_far: moneyFromCents(100_000),
        funded: moneyFromCents(100_000),
        left_to_save: moneyFromCents(0),
        monthly_needed: null,
        is_funded: true,
        is_complete: true,
      }),
    )

    expect(html).toContain('Funded')
    expect(html).toContain('count that transfer as taken out')
    expect(html).not.toContain('Left to save')
  })

  it('reads a closed goal as history, not reserved', () => {
    const html = card({ ...SPENDING, stage: 'closed', closed_on: '2026-08-20' })

    expect(html).toContain('Closed')
    expect(html).toContain('Left in Rainy Day Savings')
    expect(html).toContain('Was reserved in')
    expect(html).not.toContain('From the plan')
  })
})
