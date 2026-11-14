/** The review queue as ReviewService answers it: every tier and every reason. */

import { ACCOUNT, CATEGORY, id } from './core'

const money = (amount: string) => ({ amount })

function row(n: number, fields: Record<string, unknown>) {
  return {
    transaction_id: id('review', n),
    date: '2026-06-14',
    effective_date: '2026-06-14',
    payee: 'Corner Market',
    statement_name: 'CORNER MARKET #12',
    amount: money('-20.00'),
    account_id: ACCOUNT.card,
    account_name: 'Rewards Card',
    account_mask: '3003',
    category_name: 'Groceries',
    category_id: CATEGORY.groceries,
    splits: [],
    is_pending: false,
    reason: 'REVIEW_REASON_UNSPECIFIED',
    support: [],
    duplicate_key: '',
    notes: '',
    ...fields,
  }
}

const ITEMS = [
  {
    key: `challenge:${id('challenge', 1)}`,
    kind: 'REVIEW_KIND_SIGN_IN_CODE',
    tier: 'REVIEW_TIER_ACT_NOW',
    title: 'City Power wants a code',
    detail: 'Enter the code sent to the phone ending 1234.',
    due_at: '2026-06-15T16:04:00Z',
    subject_ids: [],
    actions: [{ action: 'REVIEW_ACTION_ANSWER_CODE', label: 'Send', confirm: false }],
    link: '/settings/bills',
    count: 0,
  },
  {
    key: `bill:${id('series', 1)}:2026-06-12`,
    kind: 'REVIEW_KIND_BILL_PAST_DUE',
    tier: 'REVIEW_TIER_MONEY_AT_STAKE',
    title: 'Riverside Water is past due',
    detail: 'Expected Jun 12 · 3 days late',
    due_at: '2026-06-12T00:00:00Z',
    amount: money('-60.00'),
    subject_ids: [id('series', 1)],
    actions: [
      { action: 'REVIEW_ACTION_MARK_PAID', label: 'Mark paid', confirm: false },
      { action: 'REVIEW_ACTION_SKIP_OCCURRENCE', label: 'Skip', confirm: false },
    ],
    link: '/upcoming',
    count: 0,
  },
  {
    key: `duplicate:${id('pair', 1)}`,
    kind: 'REVIEW_KIND_DUPLICATE',
    tier: 'REVIEW_TIER_MONEY_AT_STAKE',
    title: 'Possible duplicate: Corner Market',
    detail: '2026-06-13 and 2026-06-14 on Rewards Card, the same amount from two sources',
    amount: money('-42.10'),
    subject_ids: [id('review', 4), id('review', 5)],
    actions: [
      { action: 'REVIEW_ACTION_KEEP_SUGGESTED', label: 'Merge into one', confirm: true },
      { action: 'REVIEW_ACTION_BOTH_REAL', label: 'Both are real', confirm: true },
    ],
    link: '/settings/duplicates',
    count: 0,
  },
  {
    key: `proposal:${id('action', 50)}`,
    kind: 'REVIEW_KIND_PROPOSAL',
    tier: 'REVIEW_TIER_TIDY_UP',
    title: 'Rename "SQ *HARBOR" to Harbor Coffee on every row',
    detail: 'Payee rule',
    subject_ids: [],
    actions: [
      { action: 'REVIEW_ACTION_ACCEPT_PROPOSAL', label: 'Accept', confirm: false },
      { action: 'REVIEW_ACTION_DECLINE_PROPOSAL', label: 'Decline', confirm: false },
    ],
    link: '/assistant',
    count: 0,
  },
  {
    key: 'unmatched-orders:amazon',
    kind: 'REVIEW_KIND_UNMATCHED_ORDER',
    tier: 'REVIEW_TIER_IDEAS',
    title: '3 Amazon orders with no charge matched',
    detail: 'Match them to card charges, or ignore the ones that never were.',
    subject_ids: [],
    actions: [],
    link: '/settings/merchants/amazon',
    count: 3,
  },
]

const DAYS = [
  {
    date: '2026-06-14',
    out: money('122.10'),
    needs_a_look: 3,
    rows: [
      row(1, {}),
      row(2, {
        payee: 'A Very Long Independent Bookshop And Reading Room Cafe',
        statement_name: 'A VERY LONG INDEPENDENT BOOKSHOP AND READING',
        amount: money('-18.40'),
        category_id: null,
        category_name: '',
        reason: 'REVIEW_REASON_GUESS',
        suggestion: {
          action_id: id('action', 2),
          category_id: CATEGORY.shopping,
          category_name: 'Shopping',
          confidence: 0.55,
          splits: [],
          basis: 'payee_history',
        },
      }),
      row(3, {
        payee: 'Harbor Coffee',
        amount: money('-41.60'),
        category_id: CATEGORY.dining,
        category_name: 'Dining',
        reason: 'REVIEW_REASON_UNUSUAL_AMOUNT',
        is_pending: true,
      }),
      row(4, {
        amount: money('-42.10'),
        reason: 'REVIEW_REASON_POSSIBLE_DUPLICATE',
        duplicate_key: `duplicate:${id('pair', 1)}`,
      }),
    ],
  },
  {
    date: '2026-06-13',
    out: money('0.00'),
    needs_a_look: 1,
    rows: [
      row(6, {
        date: '2026-06-13',
        effective_date: '2026-06-13',
        payee: 'Online Transfer',
        statement_name: 'ONLINE TRANSFER REF 0000',
        amount: money('250.00'),
        account_id: ACCOUNT.checking,
        account_name: 'Everyday Checking',
        account_mask: '',
        category_id: null,
        category_name: '',
        reason: 'REVIEW_REASON_NO_CATEGORY',
      }),
    ],
  },
]

export const REVIEW = {
  'POST /agentifi.v1.ReviewService/GetReviewSummary': {
    act_now: 1,
    money_at_stake: 2,
    tidy_up: 1,
    ideas: 1,
    new_rows: 5,
    needs_a_look: 4,
    badge: 8,
    kinds: [],
  },
  'POST /agentifi.v1.ReviewService/ListReviewItems': { items: ITEMS },
  'POST /agentifi.v1.ReviewService/ListReviewDays': { days: DAYS },
}
