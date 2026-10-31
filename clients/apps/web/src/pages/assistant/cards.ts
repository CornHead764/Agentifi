/**
 * What a proposed change's card shows, as plain functions, so the page, the
 * Ask dialog and an automation's run record read the same answer for the same
 * card.
 */

import type { AssistantAction, ActionStatus } from '@/lib/clients/assistant'
import type { RuleActions, RuleCondition } from '@/lib/clients/rules'
import { registerLinkFor } from '@/lib/transactions/links'
import type { FilterUniverse } from '@/lib/transactions/filter'
import type { FilterField, FilterOperator } from '@/lib/transactions/types'
import { isPlainObject } from '@/lib/typeGuards'
import { describeActions, describeConditions, readConditions } from '@/pages/rules/conditions'

/** Where a card is in its life, in the words its badge uses. */
export type CardPhase = 'proposed' | 'running' | 'done' | 'failed' | 'declined' | 'simulated'

export function cardPhase(status: ActionStatus): CardPhase {
  switch (status) {
    case 'pending':
      return 'proposed'
    case 'applying':
      return 'running'
    case 'applied':
      return 'done'
    case 'failed':
      return 'failed'
    case 'discarded':
      return 'declined'
    case 'simulated':
      return 'simulated'
  }
}

/**
 * Whether Accept can be pressed: a card still waiting, or one the app refused.
 * A refusal changed nothing, so pressing again is a retry rather than a
 * second change.
 */
export function decidable(action: Pick<AssistantAction, 'status'>): boolean {
  return action.status === 'pending' || action.status === 'failed'
}

/** The cards "Accept all" would apply: everything still waiting, in order. */
export function waiting(actions: readonly AssistantAction[]): AssistantAction[] {
  return actions.filter((action) => action.status === 'pending')
}

/** How a group of cards stands, for the line on the group's card. */
export function groupTally(actions: readonly Pick<AssistantAction, 'status'>[]) {
  const tally = { proposed: 0, running: 0, done: 0, failed: 0, declined: 0, simulated: 0 }
  for (const action of actions) tally[cardPhase(action.status)]++
  return tally
}

/**
 * Why the app refused a card, readably. A refusal's result is
 * `{"detail": …}`: a sentence, or a list of field errors.
 */
export function failureText(result: string): string {
  const trimmed = result.trim()
  if (trimmed === '') return 'The app refused this change.'
  try {
    const parsed: unknown = JSON.parse(trimmed)
    if (typeof parsed === 'object' && parsed !== null && 'detail' in parsed) {
      const detail = parsed.detail
      if (typeof detail === 'string') return detail
      if (Array.isArray(detail)) {
        const messages = detail
          .map((one) =>
            typeof one === 'object' && one !== null && 'msg' in one ? String(one.msg) : '',
          )
          .filter((one) => one !== '')
        if (messages.length > 0) return messages.join('; ')
      }
    }
  } catch {
    // Not JSON: a sentence the server wrote, shown as it is.
  }
  return trimmed
}

/** Where a finished card leads: the thing it made or changed. */
export interface CardLink {
  to: string
  label: string
}

const RESOURCE_LINKS: Record<string, CardLink> = {
  rules: { to: '/rules', label: 'Open Rules' },
  categories: { to: '/settings/categories-tags', label: 'Open Categories' },
  tags: { to: '/settings/categories-tags', label: 'Open Tags' },
  series: { to: '/upcoming/recurring', label: 'Open Recurring' },
  accounts: { to: '/settings/accounts', label: 'Open Accounts' },
  goals: { to: '/goals', label: 'Open Goals' },
  'spending-plan': { to: '/spending-plan', label: 'Open the spending plan' },
}

const ONE_ITEM_LINKS: Record<string, CardLink> = {
  rules: { to: '/rules?rule=', label: 'Open the rule' },
  series: { to: '/upcoming/recurring?series=', label: 'Open the recurring item' },
  categories: { to: '/settings/categories-tags?category=', label: 'Open the category' },
}

export function cardLink(
  action: Pick<AssistantAction, 'status' | 'path' | 'resource_id' | 'about' | 'method'>,
): CardLink | null {
  if (action.status !== 'applied' || action.method === 'DELETE') return null
  const resource = action.path.replace(/^\//, '').split('/')[0]
  if (resource === 'transactions') {
    if (action.about) {
      return {
        to: registerLinkFor(
          { id: action.about.transaction_id, account_id: action.about.account_id, date: action.about.date },
          { open: true },
        ),
        label: 'Open the transaction',
      }
    }
    return { to: '/transactions', label: 'Open Transactions' }
  }
  if (resource === 'watchlists') {
    return action.resource_id
      ? { to: `/watchlist/${action.resource_id}`, label: 'Open the watchlist' }
      : { to: '/watchlist', label: 'Open Watchlists' }
  }
  // The lists that can open or mark one row read its id from the URL; see
  // lib/linkedItem.ts. Without an id, the list itself.
  const one = action.resource_id ? ONE_ITEM_LINKS[resource] : undefined
  if (one && action.resource_id) {
    return { to: `${one.to}${encodeURIComponent(action.resource_id)}`, label: one.label }
  }
  return RESOURCE_LINKS[resource] ?? null
}

/**
 * Every name a card may need: what the server resolved when the change was
 * proposed, over what this page already knows. The server's wins, because it
 * is what the model was talking about at the time.
 */
export function cardNames(
  action: Pick<AssistantAction, 'preview'>,
  known: readonly { id: string; name: string }[],
): Map<string, string> {
  const out = new Map<string, string>()
  for (const row of known) out.set(row.id, row.name)
  for (const [id, name] of Object.entries(action.preview?.names ?? {})) out.set(id, name)
  return out
}

/** A proposed rule, in the words the Rules page uses for it. */
export interface RuleView {
  name: string
  /** *If a transaction matches this*, one chip per condition. */
  conditions: string[]
  /** *Then make these changes*, one chip per change. */
  then: string[]
}

const FIELDS: readonly FilterField[] = [
  'category',
  'payee',
  'tag',
  'account',
  'flag',
  'text',
  'amount',
  'date',
  'is_bill_or_subscription',
  'is_excluded_from_reports',
  'is_excluded_from_spending_plan',
  'is_reviewed',
  'statement_name',
  'is_pending',
  'is_uncategorized',
  'is_category_undetermined',
  'has_category_suggestion',
  'has_tag',
  'has_attachment',
  'is_missing_receipt',
]

const OPERATORS: readonly FilterOperator[] = [
  'in',
  'contains',
  'is_exactly',
  'equals',
  'between',
  'greater_than',
  'less_than',
  'is_true',
  'matches',
]

function textOrNull(value: unknown): string | null {
  return typeof value === 'string' ? value : null
}

function flagOrNull(value: unknown): boolean | null {
  return typeof value === 'boolean' ? value : null
}

function texts(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((one): one is string => typeof one === 'string') : []
}

/**
 * One stored clause, read field by field. A clause whose field this client
 * does not know is left out rather than drawn as something it is not.
 */
function readCondition(raw: unknown): RuleCondition | null {
  if (!isPlainObject(raw)) return null
  const field = FIELDS.find((one) => one === raw.field)
  if (field === undefined) return null
  const operator = OPERATORS.find((one) => one === raw.operator) ?? 'in'
  return {
    field,
    operator,
    group_index: typeof raw.group_index === 'number' ? raw.group_index : 0,
    position: typeof raw.position === 'number' ? raw.position : 0,
    negated: raw.negated === true,
    value_ids: texts(raw.value_ids),
    value_texts: texts(raw.value_texts),
    text: textOrNull(raw.text),
    amount_min: textOrNull(raw.amount_min),
    amount_max: textOrNull(raw.amount_max),
    date_from: textOrNull(raw.date_from),
    date_to: textOrNull(raw.date_to),
    date_preset: textOrNull(raw.date_preset),
    state: flagOrNull(raw.state),
  }
}

function readActions(raw: unknown): RuleActions {
  const actions = isPlainObject(raw) ? raw : {}
  return {
    set_payee: textOrNull(actions.set_payee),
    set_category_id: textOrNull(actions.set_category_id),
    add_tag_ids: texts(actions.add_tag_ids),
    set_notes: textOrNull(actions.set_notes),
    set_excluded_from_reports: flagOrNull(actions.set_excluded_from_reports),
    set_excluded_from_spending_plan: flagOrNull(actions.set_excluded_from_spending_plan),
    set_is_reviewed: flagOrNull(actions.set_is_reviewed),
  }
}

/**
 * A create or update of a rule, read back as the Rules page draws it: through
 * the same `readConditions` and `describeConditions` (ground rule 3), with
 * names. Null for any other card.
 */
export function ruleView(
  action: Pick<AssistantAction, 'path' | 'body' | 'method'>,
  universe: FilterUniverse,
  names: ReadonlyMap<string, string>,
): RuleView | null {
  if (!/^\/rules(\/[^/]+)?$/.test(action.path) || action.method === 'DELETE') return null
  const body = action.body ?? {}
  const conditions = (Array.isArray(body.conditions) ? body.conditions : []).flatMap((raw) => {
    const condition = readCondition(raw)
    return condition === null ? [] : [condition]
  })
  const actions = readActions(body.actions)
  return {
    name: typeof body.name === 'string' ? body.name : '',
    conditions:
      conditions.length === 0 ? [] : describeConditions(readConditions(conditions, universe), names),
    then: describeActions(actions, names),
  }
}
