import { afterEach, describe, expect, it, vi } from 'vitest'

import { auditUndeclaredMoney, coerceMoney } from './api'
import {
  GOAL_SHAPE,
  GOAL_TEMPLATES,
  blankGoalEdits,
  editsFromGoal,
  fundingAccounts,
  goalBarSegments,
  goalBodyFromEdits,
  goalCandidates,
  goalCounting,
  reservedInAccount,
  type Goal,
  type GoalEdits,
  type GoalMove,
} from './goals'
import { moneyFromCents } from './money'

function goal(overrides: Partial<Goal> = {}): Goal {
  return {
    id: 'g1',
    name: 'Japan Trip',
    emoji: '🏝',
    account_id: 'savings',
    account_name: 'Rainy Day Savings',
    funding_account_ids: [],
    funding: [],
    target_amount: moneyFromCents(1_500_000),
    target_on: null,
    completed_on: null,
    closed_on: null,
    stage: 'saving',
    is_taken_from_plan: true,
    saved_so_far: moneyFromCents(0),
    withdrawn: moneyFromCents(0),
    spent_on_goal: moneyFromCents(0),
    spending_by_category: [],
    unassigned_withdrawn: moneyFromCents(0),
    funded: moneyFromCents(0),
    contributed_this_month: moneyFromCents(0),
    left_to_save: moneyFromCents(1_500_000),
    monthly_needed: null,
    months_to_target: null,
    target_has_passed: false,
    pct_complete: '0',
    pct_funded: '0',
    is_complete: false,
    is_funded: false,
    txn_ids: [],
    withdrawal_txn_ids: [],
    spending_txn_ids: [],
    contributions: [],
    ...overrides,
  }
}

/** One goal as `GET /goals` sends it, from `TestTheFundingRowsSayWhatEachAccountPutIn`. */
const WIRE_GOAL = {
  id: '1a1e6c8e-0f0b-4d1a-9d55-2d4b6b1b0001',
  name: 'Emergency Fund',
  emoji: null,
  account_id: 'acc-checking',
  account_name: 'Everyday Checking',
  funding_account_ids: ['acc-checking', 'acc-card'],
  funding: [
    { account_id: 'acc-checking', account_name: 'Everyday Checking', saved: '40.00' },
    { account_id: 'acc-card', account_name: 'Rewards Card', saved: '0.00' },
  ],
  target_amount: '1000.00',
  target_on: '2026-12-31',
  completed_on: null,
  closed_on: null,
  stage: 'saving',
  is_taken_from_plan: true,
  saved_so_far: '40.00',
  withdrawn: '0.00',
  spent_on_goal: '25.00',
  unassigned_withdrawn: '0.00',
  spending_by_category: [
    { category_id: 'cat-air', category_name: 'Airfare', spent: '20.00', transaction_count: 1 },
    { category_id: null, category_name: 'Uncategorized', spent: '5.00', transaction_count: 1 },
  ],
  funded: '40.00',
  left_to_save: '960.00',
  contributed_this_month: '40.00',
  pct_complete: '4',
  pct_funded: '4',
  monthly_needed: '192.00',
  months_to_target: 5,
  target_has_passed: false,
  is_complete: false,
  is_funded: false,
  txn_ids: ['txn-1'],
  contributions: [
    {
      transaction_id: 'txn-1',
      date: '2026-08-08',
      account_id: 'acc-checking',
      account_name: 'Everyday Checking',
      payee: 'Savings',
      amount: '-40.00',
      saved: '40.00',
    },
  ],
}

describe('a goal off the wire', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('carries funding as rows, not as a flat list of ids', () => {
    const [parsed] = coerceMoney<Goal[]>([structuredClone(WIRE_GOAL)], GOAL_SHAPE)

    expect(parsed.funding).toHaveLength(2)
    expect(parsed.funding[0].account_name).toBe('Everyday Checking')
    expect(parsed.funding[0].saved).toBe(moneyFromCents(4_000))
    expect(parsed.funding[1].saved).toBe(moneyFromCents(0))
  })

  it('names the account it reserves in, and what has come back out', () => {
    const [parsed] = coerceMoney<Goal[]>([structuredClone(WIRE_GOAL)], GOAL_SHAPE)

    expect(parsed.account_name).toBe('Everyday Checking')
    expect(parsed.withdrawn).toBe(moneyFromCents(0))
    expect(parsed.completed_on).toBeNull()
  })

  // The breakdown is why a goal charge keeps its ordinary category, so its
  // amounts have to arrive as money rather than as the strings the wire sends.
  it('coerces the breakdown, uncategorized line included', () => {
    const [parsed] = coerceMoney<Goal[]>([structuredClone(WIRE_GOAL)], GOAL_SHAPE)

    expect(parsed.spending_by_category).toHaveLength(2)
    expect(parsed.spending_by_category[0].category_name).toBe('Airfare')
    expect(parsed.spending_by_category[0].spent).toBe(moneyFromCents(2_000))
    expect(parsed.spending_by_category[1].category_id).toBeNull()
    expect(parsed.spending_by_category[1].spent).toBe(moneyFromCents(500))
  })

  it('declares every money field it is sent, so none reaches a screen as a string', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const parsed = coerceMoney<Goal[]>([structuredClone(WIRE_GOAL)], GOAL_SHAPE)

    auditUndeclaredMoney(parsed)
    expect(warn).not.toHaveBeenCalled()
  })
})

/* The derived figures are the server's; their tests are internal/domain/goals_test.go. */

describe('goalBarSegments', () => {
  it('splits the bar into what is held and what was held and spent, without float drift', () => {
    // 33.3 - 33.2 is 0.09999999999999432 in floating point.
    expect(goalBarSegments(goal({ pct_complete: '33.2', pct_funded: '33.3' }))).toEqual({
      saved: 33.2,
      spent: 0.1,
      reached: 33.3,
    })
    expect(goalBarSegments(goal({ pct_complete: '40', pct_funded: '100' }))).toEqual({
      saved: 40,
      spent: 60,
      reached: 100,
    })
  })

  it('draws nothing for a target of zero, and never past the end of the track', () => {
    expect(goalBarSegments(goal({ pct_complete: null, pct_funded: null }))).toEqual({
      saved: 0,
      spent: 0,
      reached: null,
    })
    expect(goalBarSegments(goal({ pct_complete: '80', pct_funded: '120' }))).toEqual({
      saved: 80,
      spent: 20,
      reached: 120,
    })
    expect(goalBarSegments(goal({ pct_complete: '50', pct_funded: '45' })).spent).toBe(0)
  })
})

describe('the account reserve', () => {
  it('counts only the goals that name the account, however many fund them', () => {
    // Reserving in every funding account would subtract the same savings from each.
    const goals = [
      goal({ id: 'a', account_id: 'savings', saved_so_far: moneyFromCents(1_500_000) }),
      goal({
        id: 'b',
        account_id: 'brokerage',
        saved_so_far: moneyFromCents(200_000),
        funding: [
          { account_id: 'savings', account_name: 'Rainy Day Savings', saved: moneyFromCents(200_000) },
        ],
      }),
    ]

    expect(reservedInAccount('savings', goals)).toBe(moneyFromCents(1_500_000))
    expect(reservedInAccount('brokerage', goals)).toBe(moneyFromCents(200_000))
  })

  it('leaves out a closed goal, whose money is available again', () => {
    const goals = [
      goal({ id: 'a', saved_so_far: moneyFromCents(30_000) }),
      goal({ id: 'b', saved_so_far: moneyFromCents(12_500), stage: 'closed', closed_on: '2026-08-20' }),
    ]

    expect(reservedInAccount('savings', goals)).toBe(moneyFromCents(30_000))
  })
})

describe('blankGoalEdits', () => {
  it('defaults is_taken_from_plan to true, matching what an omitted create gets', () => {
    expect(blankGoalEdits().is_taken_from_plan).toBe(true)
  })
})

describe('editsFromGoal', () => {
  it('reads the target back as a wire string, not a float', () => {
    const edits = editsFromGoal(goal({ target_amount: moneyFromCents(150_000) }))
    expect(edits.target_amount).toBe('1500.00')
  })

  it('seeds a missing emoji as an empty string, for a controlled input', () => {
    expect(editsFromGoal(goal({ emoji: null })).emoji).toBe('')
  })
})

describe('goalBodyFromEdits', () => {
  const filled = (patch: Partial<GoalEdits> = {}): GoalEdits => ({
    ...blankGoalEdits(),
    name: 'Emergency Fund',
    account_id: 'acc-checking',
    ...patch,
  })

  it('refuses an amount that does not parse, rather than sending garbage', () => {
    expect(goalBodyFromEdits(filled({ target_amount: 'lots' }))).toBeNull()
  })

  it('parses a typed amount onto the wire without ever touching a float', () => {
    const body = goalBodyFromEdits(filled({ target_amount: '1,500' }))
    expect(body?.target_amount).toBe('1500.00')
  })

  it('trims the name', () => {
    expect(goalBodyFromEdits(filled({ name: '  Emergency Fund  ' }))?.name).toBe('Emergency Fund')
  })

  it('sends a blank emoji and an unset target date as null, not empty strings', () => {
    const body = goalBodyFromEdits(filled({ emoji: '   ', target_on: null }))
    expect(body?.emoji).toBeNull()
    expect(body?.target_on).toBeNull()
  })

  // An empty list would clear whatever the import already linked.
  it('leaves txn_ids off entirely', () => {
    expect(goalBodyFromEdits(filled())).not.toHaveProperty('txn_ids')
  })
})

describe('the accounts a goal is funded from', () => {
  const filled = (patch: Partial<GoalEdits> = {}): GoalEdits => ({
    ...blankGoalEdits(),
    name: 'Emergency Fund',
    account_id: 'acc-checking',
    ...patch,
  })

  it('always includes the account holding the reserve, and leads with it', () => {
    expect(fundingAccounts(filled())).toEqual(['acc-checking'])
    expect(fundingAccounts(filled({ funding_account_ids: ['acc-savings'] }))).toEqual([
      'acc-checking',
      'acc-savings',
    ])
  })

  it('does not list the reserve account twice when it is also ticked', () => {
    expect(
      fundingAccounts(filled({ funding_account_ids: ['acc-checking', 'acc-savings'] })),
    ).toEqual(['acc-checking', 'acc-savings'])
  })

  it('sends nothing at all before an account has been chosen', () => {
    expect(fundingAccounts(filled({ account_id: '', funding_account_ids: [] }))).toEqual([])
  })

  it('rides out to the server on the create body', () => {
    const body = goalBodyFromEdits(filled({ funding_account_ids: ['acc-savings'] }))
    expect(body?.funding_account_ids).toEqual(['acc-checking', 'acc-savings'])
  })

  it('reads the several funders of an imported goal back into the form', () => {
    const edits = editsFromGoal(goal({ funding_account_ids: ['savings', 'checking'] }))
    expect(edits.funding_account_ids).toEqual(['savings', 'checking'])
  })
})

describe('the goal templates', () => {
  it('seeds a name and a glyph from the template that was picked', () => {
    const vacation = GOAL_TEMPLATES.find((one) => one.key === 'vacation')
    const edits = blankGoalEdits(vacation)
    expect(edits.name).toBe('Vacation')
    expect(edits.emoji).toBe('🏖️')
  })

  it('leaves the name empty for Custom, while still lending its glyph', () => {
    const custom = GOAL_TEMPLATES.find((one) => one.key === 'custom')
    const edits = blankGoalEdits(custom)
    expect(edits.name).toBe('')
    expect(edits.emoji).toBe('💰')
  })

  it('offers the six Simplifi offers, with Custom last', () => {
    expect(GOAL_TEMPLATES.map((one) => one.label)).toEqual([
      'Emergency Fund',
      'Car',
      'Vacation',
      'Home',
      'Wedding',
      'Custom',
    ])
  })
})

describe('goalCandidates', () => {
  const row = (
    id: string,
    cents: number,
    payee: string,
    statement = payee.toUpperCase(),
    account_id = 'checking',
  ) => ({ id, account_id, amount: moneyFromCents(cents), payee, statement_name: statement })

  // The goal reserves in Rainy Day Savings and is funded from checking, so the
  // last two rows sit on the account it reserves in and the rest do not.
  const rows = [
    row('out', -1_500_000, 'Savings', 'TRANSFER TO SAVINGS'),
    row('in', 1_500_000, 'Checking', 'TRANSFER FROM SAVINGS'),
    row('coffee', -450, 'Corner Store'),
    row('zero', 0, 'Void'),
    row('deposit', 1_500_000, 'Savings', 'TRANSFER FROM CHECKING', 'savings'),
    row('resort', -500_000, 'Island Resort', 'ISLAND RESORT', 'savings'),
  ]
  const trip = { id: 'g1', account_id: 'savings' }
  const ids = (move: GoalMove, search = '', goals: Goal[] = []) =>
    goalCandidates(rows, move, search, goals, trip).map((one) => one.row.id)

  it('narrows on the account the goal reserves in, where the sign is decisive', () => {
    expect(ids('contribute')).toContain('deposit')
    expect(ids('contribute')).not.toContain('resort')
    expect(ids('withdraw')).toContain('resort')
    expect(ids('withdraw')).not.toContain('deposit')
  })

  // 15,000 leaving checking may be a contribution or a withdrawal; the row is
  // identical either way, so both moves offer it.
  it('offers a row on a funding account either way, because its sign settles nothing', () => {
    expect(ids('contribute')).toEqual(expect.arrayContaining(['out', 'in', 'coffee']))
    expect(ids('withdraw')).toEqual(expect.arrayContaining(['out', 'in', 'coffee']))
  })

  it('never offers a row of nothing', () => {
    expect(ids('contribute')).not.toContain('zero')
    expect(ids('withdraw')).not.toContain('zero')
    expect(ids('spend')).not.toContain('zero')
  })

  // Spending is narrowed by nothing: a refund against a charge must be linkable.
  it('offers every row for spending, on the goal own account included', () => {
    expect(ids('spend').sort()).toEqual(['coffee', 'deposit', 'in', 'out', 'resort'])
  })

  it('leaves out rows this goal already counts and marks rows another goal does', () => {
    const goals = [
      goal({ id: 'g1', account_id: 'savings', txn_ids: ['out'] }),
      goal({ id: 'g2', name: 'Car', txn_ids: ['coffee'] }),
    ]
    const out = goalCandidates(rows, 'contribute', '', goals, trip)
    expect(out.map((one) => one.row.id)).not.toContain('out')
    expect(out.find((one) => one.row.id === 'coffee')?.elsewhere?.name).toBe('Car')
  })

  it('searches the name, the bank wording and the whole-dollar amount', () => {
    expect(ids('contribute', 'corner')).toEqual(['coffee'])
    expect(ids('contribute', 'TRANSFER')).toEqual(['out', 'in', 'deposit'])
    // 'deposit' is 15,000 too, and drops out because money arriving in the
    // account the goal reserves in cannot be a withdrawal.
    expect(ids('withdraw', '15000')).toEqual(['out', 'in'])
    expect(ids('withdraw', '15,000')).toEqual([])
  })
})

describe('goalCounting', () => {
  const goals = [
    goal({
      id: 'g1',
      txn_ids: ['out', 'spent', 'flights'],
      withdrawal_txn_ids: ['spent'],
      spending_txn_ids: ['flights'],
    }),
    goal({ id: 'g2', name: 'Car', txn_ids: [] }),
  ]

  it('reads the direction a counted row was filed under, not its sign', () => {
    expect(goalCounting({ id: 'out', amount: moneyFromCents(-100) }, goals)).toEqual({
      goal: goals[0],
      move: 'contribute',
    })
    // Negative, like the row above, and a withdrawal because that is what was
    // recorded — money spent on what the goal was saving for.
    expect(goalCounting({ id: 'spent', amount: moneyFromCents(-500_000) }, goals)).toEqual({
      goal: goals[0],
      move: 'withdraw',
    })
    // Negative again, and neither of the above: a card charge the goal only
    // records, which leaves the reserve alone.
    expect(goalCounting({ id: 'flights', amount: moneyFromCents(-400_000) }, goals)).toEqual({
      goal: goals[0],
      move: 'spend',
    })
  })

  it('suggests a direction from the sign for a row counted toward nothing', () => {
    expect(goalCounting({ id: 'other', amount: moneyFromCents(250) }, goals)).toEqual({
      goal: null,
      move: 'withdraw',
    })
    expect(goalCounting({ id: 'other', amount: moneyFromCents(0) }, goals).move).toBeNull()
  })
})
