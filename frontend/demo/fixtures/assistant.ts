/**
 * The assistant: a past conversation, the one a scene asks, the cards it
 * proposes, the category check the register fires, and the automation behind it.
 */

import { USER_ID } from './auth'
import { CATEGORY, categoryName, id, parentOf } from './core'
import { SHOWCASE, SUGGESTED, TRANSACTIONS, byNumber, isSpending, suggestionNumber } from './ledger'
import { ruleActions } from './planning'
import { state } from './state'

const ASKED_AT = '2026-06-15T16:00:00Z'
const ANSWERED_AT = '2026-06-15T16:00:20Z'
const NEW_CONVERSATION = id('conv', 3)
const GROUP = id('group', 1)

function message(n: number, role: 'user' | 'assistant' | 'tool', content: string, at: string, tool = '', args: Record<string, unknown> | null = null) {
  return { id: id('msg', n), role, content, tool_name: tool, tool_arguments: args, created_at: at }
}

/* ---- What the scene asks, and the answer --------------------------------- */

export const QUESTION = 'Where did our money go this month? And can you tidy up anything uncategorized?'

function juneByCategory(): [string, number][] {
  const totals = new Map<string, number>()
  for (const txn of TRANSACTIONS) {
    if (!txn.date.startsWith('2026-06') || !isSpending(txn)) continue
    const parts = txn.splits.length > 0 ? txn.splits.map((split) => ({ category: split.category_id, amount: split.amount })) : [{ category: txn.category_id, amount: txn.amount }]
    for (const part of parts) {
      if (part.category === null) continue
      const top = parentOf(part.category) ?? part.category
      totals.set(top, (totals.get(top) ?? 0) - Number(part.amount))
    }
  }
  return [...totals.entries()].sort((a, b) => b[1] - a[1])
}

function dollars(value: number): string {
  return `$${value.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

const WHY: Record<string, string> = {
  [CATEGORY.home]: 'nearly all of it the mortgage',
  [CATEGORY.travel]: 'the flights and beach rental for the summer trip, still inside its $800.00 envelope',
  [CATEGORY.food]: 'Costco and Example Market are most of it',
  [CATEGORY.auto]: 'the car payment and fuel',
}

function answerText(): string {
  const top = juneByCategory()
  const total = top.reduce((all, [, value]) => all + value, 0)
  const lines = top.slice(0, 3).map(([key, value], index) => {
    const lead = index === 0 ? 'is the biggest share at' : index === 1 ? 'comes next at' : 'is'
    return `- **${categoryName(key)}** ${lead} ${dollars(value)}${WHY[key] ? `: ${WHY[key]}` : ''}.`
  })
  return [
    `So far in June you've spent **${dollars(total)}**, against **$6,500.00** of pay.`,
    '',
    ...lines,
    '',
    'Five June transactions have no category. I\'ve proposed one for each below, and a rule so Sample Noodle Bar files itself next time. Nothing changes until you accept.',
  ].join('\n')
}

const UNCATEGORIZED = [SHOWCASE.bakery, SHOWCASE.hardware, SHOWCASE.noodles, SHOWCASE.games, SHOWCASE.parking]

function categorize(n: number, index: number) {
  const txn = byNumber(n)
  const category = SUGGESTED[n].category
  return {
    id: id('action', 100 + n),
    tool: 'update_transaction',
    summary: `File ${txn.payee} under ${categoryName(category)}`,
    method: 'PATCH',
    path: `/transactions/${txn.id}`,
    body: { category_id: category },
    created_at: `2026-06-15T16:00:${String(21 + index).padStart(2, '0')}Z`,
    about: { transaction_id: txn.id, account_id: txn.account_id, date: txn.date, statement_name: txn.statement_name, payee: txn.payee, amount: txn.amount },
    preview: {
      resource: 'transactions',
      verb: 'update',
      fields: [],
      names: { [category]: categoryName(category) },
      group_summary: 'Categorize 5 June transactions',
    },
    group_id: GROUP,
    resource_id: txn.id,
  }
}

const RULE_ACTION = {
  id: id('action', 200),
  tool: 'create_rule',
  summary: 'Make a rule: Sample Noodle Bar is Restaurants',
  method: 'POST',
  path: '/rules',
  body: {
    name: 'Sample Noodle Bar',
    conditions: [
      {
        field: 'payee',
        operator: 'in',
        group_index: 0,
        position: 0,
        negated: false,
        value_ids: [],
        value_texts: ['Sample Noodle Bar'],
        text: null,
        amount_min: null,
        amount_max: null,
        date_from: null,
        date_to: null,
        date_preset: null,
        state: null,
      },
    ],
    actions: ruleActions({ set_category_id: CATEGORY.dining }),
  },
  created_at: '2026-06-15T16:00:30Z',
  about: null,
  preview: {
    resource: 'rules',
    verb: 'create',
    fields: [],
    names: { [CATEGORY.dining]: 'Restaurants' },
  },
  group_id: null,
  resource_id: id('rule', 7),
}

const PROPOSALS = [...UNCATEGORIZED.map(categorize), RULE_ACTION]

function decided<T extends { id: string }>(action: T) {
  const outcome = state.decided.get(action.id)
  return {
    ...action,
    status: outcome ?? 'pending',
    result: outcome === 'applied' ? 'Done.' : '',
    status_code: outcome === 'applied' ? 200 : 0,
    decided_at: outcome ? '2026-06-15T16:01:00Z' : null,
    decline_reason: '',
  }
}

function askedConversation() {
  return {
    id: NEW_CONVERSATION,
    title: 'Where did our money go this month?',
    created_at: ASKED_AT,
    updated_at: ANSWERED_AT,
    messages: [
      message(10, 'user', QUESTION, ASKED_AT),
      message(11, 'tool', '', '2026-06-15T16:00:05Z', 'spending_report', { from: '2026-06-01', to: '2026-06-15' }),
      message(12, 'tool', '', '2026-06-15T16:00:08Z', 'list_transactions', { uncategorized: true, from: '2026-06-01' }),
      message(13, 'tool', '', '2026-06-15T16:00:11Z', 'list_rules', {}),
      message(14, 'assistant', answerText(), ANSWERED_AT),
    ],
    actions: PROPOSALS.map(decided),
    mail: null,
  }
}

function pastConversation(n: number, title: string, at: string, question: string, answer: string) {
  return {
    id: id('conv', n),
    title,
    created_at: at,
    updated_at: at,
    messages: [message(n * 10 + 1, 'user', question, at), message(n * 10 + 2, 'assistant', answer, at)],
    actions: [],
    mail: null,
  }
}

const PAST = [
  pastConversation(1, 'Can we afford the summer trip?', '2026-06-08T19:00:00Z', 'Can we afford the summer trip if we keep saving at this rate?', 'Yes. The Summer trip goal has $2,400.00 of $3,000.00 and you add $400.00 a month, so it is fully funded by July 15, two weeks before the August 1 target.'),
  pastConversation(2, 'Groceries compared with last spring', '2026-05-30T19:00:00Z', 'Are we spending more on groceries than last spring?', 'A little: May groceries were about 6% above the March to April average, mostly larger Costco trips.'),
]

function conversations() {
  const past = PAST.map((one) => ({ ...one, messages: [], actions: [] }))
  return state.asked ? [{ ...askedConversation(), messages: [], actions: [] }, ...past] : past
}

function conversation(_query: URLSearchParams, _body: unknown, path: string) {
  const conversationId = path.split('/')[3]
  if (conversationId === NEW_CONVERSATION) {
    return state.asked ? askedConversation() : { id: NEW_CONVERSATION, title: 'New conversation', created_at: ASKED_AT, updated_at: ASKED_AT, messages: [], actions: [], mail: null }
  }
  return PAST.find((one) => one.id === conversationId) ?? PAST[0]
}

function ask() {
  state.asked = true
  const answered = askedConversation()
  return { status: 200, body: { answer: answerText(), tool_calls: ['spending_report', 'list_transactions', 'list_rules'], conversation: answered }, delayMs: 2400 }
}

/* ---- Accepting and declining --------------------------------------------- */

function decide(actionId: string, outcome: 'applied' | 'discarded') {
  const n = suggestionNumber(actionId)
  if (n !== null) {
    if (outcome === 'applied') state.applied.add(n)
    const txn = byNumber(n)
    return {
      id: actionId,
      tool: 'update_transaction',
      summary: SUGGESTED[n].summary,
      method: 'PATCH',
      path: `/transactions/${txn.id}`,
      body: { category_id: SUGGESTED[n].category },
      status: outcome,
      result: '',
      status_code: 200,
      created_at: '2026-06-15T15:00:00Z',
      decided_at: '2026-06-15T16:01:00Z',
      resource_id: txn.id,
    }
  }
  state.decided.set(actionId, outcome)
  return decided(PROPOSALS.find((one) => one.id === actionId) ?? PROPOSALS[0])
}

function actionIdFrom(path: string): string {
  return path.split('/')[2]
}

function ids(body: unknown): string[] {
  if (typeof body !== 'object' || body === null || !('action_ids' in body) || !Array.isArray(body.action_ids)) return []
  return body.action_ids.filter((one): one is string => typeof one === 'string')
}

/* ---- The category check the register fires ------------------------------ */

function fire(_query: URLSearchParams, body: unknown) {
  const wanted =
    typeof body === 'object' && body !== null && 'transaction_ids' in body && Array.isArray(body.transaction_ids)
      ? body.transaction_ids.filter((one): one is string => typeof one === 'string')
      : UNCATEGORIZED.map((n) => byNumber(n).id)
  const now = Date.now()
  wanted.forEach((txnId, index) => state.checks.set(txnId, now + index * 300))
  return { rows: wanted.length, queued: wanted.length, batch_id: id('batch', 2) }
}

function latestBatch() {
  if (state.checks.size === 0) return { batch: null }
  const now = Date.now()
  const rows = state.checks.size
  const done = [...state.checks.values()].filter((asked) => now - asked >= 1800).length
  return {
    batch: {
      id: id('batch', 2),
      created_at: '2026-06-15T16:00:00Z',
      cancelled: false,
      dismissed: false,
      rows,
      done,
      pending: rows - done,
      not_run: 0,
      finished: done === rows,
      reviewed: { agreed: 0, differs: 0, unsure: 0, suggested: 0, undetermined: 0, skipped: 0, failed: 0 },
      unreviewed: { agreed: 0, differs: 0, unsure: 0, suggested: done, undetermined: 0, skipped: 0, failed: 0 },
    },
  }
}

const CONTEXT = {
  transaction: true,
  similar_transactions: 5,
  categories: true,
  rules: true,
  accounts: false,
  corrections: true,
  guidance: true,
  cash_flow_history: false,
}

const AUTOMATIONS = [
  {
    id: id('auto', 1),
    name: 'Categorize new transactions',
    description: 'Suggests a category for each new transaction.',
    is_enabled: true,
    trigger: 'transaction_arrived',
    trigger_config: { skip_transfers: true },
    filter_id: null,
    filter: null,
    prompt: 'Pick the best category for this transaction.',
    context: CONTEXT,
    mode: 'propose',
    tools: ['update_transaction'],
    model: '',
    max_tool_rounds: 4,
    confidence_threshold: 0.8,
    template_key: '',
    created_by: USER_ID,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-06-01T00:00:00Z',
    runs: 214,
    last_run_at: '2026-06-15T06:00:00Z',
    last_status: 'succeeded',
    pending_actions: 0,
  },
]

export const ASSISTANT = {
  'GET /assistant': {
    configured: true,
    is_enabled: true,
    base_url: 'http://localhost:8001/v1',
    model: 'local-model',
    name: 'Assistant',
    has_key: false,
    allow_writes: true,
    apply_without_asking: false,
    tool_call_style: 'native',
    tools: [
      { name: 'list_transactions', description: 'Read transactions.', writes: false },
      { name: 'spending_report', description: 'Read the spending report.', writes: false },
      { name: 'list_rules', description: 'Read the rules.', writes: false },
      { name: 'update_transaction', description: 'Change a transaction.', writes: true },
      { name: 'create_rule', description: 'Make a rule.', writes: true },
    ],
  },
  'GET /assistant/conversations': conversations,
  'GET /assistant/conversations/:id': conversation,
  'POST /assistant/conversations': { id: NEW_CONVERSATION, title: 'New conversation', created_at: ASKED_AT, updated_at: ASKED_AT, messages: [], actions: [], mail: null },
  'POST /assistant/conversations/:id/ask': ask,
  'POST /assistant-actions/apply-many': (_query: URLSearchParams, body: unknown) => ({ actions: ids(body).map((one) => decide(one, 'applied')) }),
  'POST /assistant-actions/:id/apply': (_query: URLSearchParams, _body: unknown, path: string) => decide(actionIdFrom(path), 'applied'),
  'POST /assistant-actions/:id/discard': (_query: URLSearchParams, _body: unknown, path: string) => decide(actionIdFrom(path), 'discarded'),
  'POST /assistant-automations/fire': fire,
  'GET /category-suggestion-batches/latest': latestBatch,
  'GET /assistant-automations': AUTOMATIONS,
  'GET /assistant-automations/templates': [],
  'GET /assistant-automations/runs': [],
  'GET /assistant-automations/pending': [],
}
