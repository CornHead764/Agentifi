/**
 * The builder's *If a transaction matches this* column, to and from
 * `RuleCondition` rows, plus the one-line rendering the list prints.
 *
 * The name half (statement name or payee, a comparison, keyword chips) is the
 * builder's own. Everything else is the register's `FilterDraft`, encoded by
 * `toFilterItems` and read by `fromFilterItems` (ground rule 3).
 *
 * **Items in one group are AND'd; the groups are OR'd.** **+** adds an
 * alternative keyword set, so every facet item is repeated into every group:
 * a facet alone in group 0 would be an alternative, not a condition on all.
 */

import { formatMoney, parseMoney } from '@/lib/money'
import {
  amountBound,
  amountOperator,
  toFilterItems,
  EMPTY_DRAFT,
  type AmountFacet,
  type FilterDraft,
  type FilterUniverse,
} from '@/lib/transactions/filter'
import { fromFilterItems } from '@/lib/reports/savedFilter'
import type { RuleActions, RuleCondition } from '@/lib/clients/rules'
import type { Uuid } from '@/lib/transactions/types'

/** Which of the two names the rule matches on. */
export type NameField = 'statement_name' | 'payee'

export type NameOperator = 'contains' | 'is_exactly' | 'matches'

/**
 * `keywordGroups` is a list of alternatives, each a list of keywords that must
 * all be present. `facets` is everything else, in the register's draft shape.
 */
export interface ConditionDraft {
  nameField: NameField
  nameOperator: NameOperator
  keywordGroups: string[][]
  facets: FilterDraft
}

export const EMPTY_CONDITIONS: ConditionDraft = {
  nameField: 'statement_name',
  nameOperator: 'contains',
  keywordGroups: [[]],
  facets: EMPTY_DRAFT,
}

/** A transaction the user pressed *Create a rule* on: only the fields the seed reads. */
export interface RuleSeed {
  statement_name: string
  payee: string
  category_id: Uuid | null
}

/**
 * One keyword group's distinct words, in order added. A repeated word narrows
 * nothing, and since chips are keyed by the word, a repeated key stops React
 * reconciling: one × deletes both and what saves is not what is shown. Bank
 * wording repeats ("CHECKCARD … CHECKCARD"), so the seed and the keyword box
 * both come through here.
 */
export function distinctKeywords(keywords: readonly string[]): string[] {
  const out: string[] = []
  for (const keyword of keywords) {
    const word = keyword.trim()
    if (word !== '' && !out.includes(word)) out.push(word)
  }
  return out
}

/**
 * The keywords to open the builder with. Bank wording is a stable payee plus a
 * varying tail (an authorization id, a date, a store number), so every token
 * containing a digit is dropped, the same rule the server's suggestion engine
 * applies. A name that is all tail keeps its wording rather than seeding
 * nothing.
 */
export function seedKeywords(statementName: string): string[] {
  const words = statementName.split(/\s+/).filter((word) => word !== '')
  const stable = distinctKeywords(words.filter((word) => !/\d/.test(word) && /[a-z]/i.test(word)))
  if (stable.length > 0) return stable
  const whole = statementName.trim()
  return whole === '' ? [] : [whole]
}

/**
 * Open the builder on one transaction. Matched on the statement name, which
 * keeps working after the rule's own rename lands, and not narrowed to the
 * account: a rule about a payee should fire wherever that payee is paid.
 */
export function seedConditions(seed: RuleSeed): ConditionDraft {
  return {
    ...EMPTY_CONDITIONS,
    nameField: 'statement_name',
    nameOperator: 'contains',
    keywordGroups: [seedKeywords(seed.statement_name)],
  }
}

/** Everything a condition needs beyond its field, operator and values. */
const BLANK: Omit<RuleCondition, 'field' | 'operator' | 'group_index'> = {
  position: 0,
  negated: false,
  value_ids: [],
  value_texts: [],
  text: null,
  amount_min: null,
  amount_max: null,
  date_from: null,
  date_to: null,
  date_preset: null,
  state: null,
}

/**
 * The clauses this draft saves as, or `null` when it would save none. An empty
 * filter would rewrite every row on a rule and never be said on a note; the
 * server refuses it too, and this lets the dialog say so first.
 *
 * The universe lets a facet mean what the register means: "uncategorized" is
 * stored as *category is not any of [every category]*, and a chosen parent is
 * expanded to its children.
 */
export function buildConditions(
  draft: ConditionDraft,
  universe: FilterUniverse,
): RuleCondition[] | null {
  const groups = draft.keywordGroups
    .map((keywords) => keywords.map((word) => word.trim()).filter(Boolean))
    .filter((keywords) => keywords.length > 0)

  const shared = toFilterItems(draft.facets, universe)
  if (groups.length === 0 && shared.length === 0) return null

  // No keywords at all still saves: "every card charge over $200" is a rule.
  const alternatives = groups.length === 0 ? [null] : groups
  const items: RuleCondition[] = []
  alternatives.forEach((keywords, group) => {
    if (keywords !== null) {
      items.push({
        ...BLANK,
        field: draft.nameField,
        operator: draft.nameOperator,
        group_index: group,
        value_texts: keywords,
      })
    }
    for (const item of shared) items.push({ ...item, group_index: group })
  })
  return items.map((item, position) => ({ ...item, position }))
}

/** Read stored clauses back into the shape the dialog edits. */
export function readConditions(
  items: readonly RuleCondition[],
  universe: FilterUniverse,
): ConditionDraft {
  const keywordGroups: string[][] = []
  let nameField: NameField = EMPTY_CONDITIONS.nameField
  let nameOperator: NameOperator = EMPTY_CONDITIONS.nameOperator

  const byGroup = new Map<number, RuleCondition[]>()
  for (const item of items) {
    byGroup.set(item.group_index, [...(byGroup.get(item.group_index) ?? []), item])
  }

  // The facets are read from one group only: `buildConditions` writes the same
  // ones into every group, so reading them all would union a set with itself
  // and, on a hand-written filter whose groups differ, quietly widen it.
  const groups = [...byGroup.keys()].sort((a, b) => a - b)
  const facetItems: RuleCondition[] = []
  for (const group of groups) {
    for (const item of byGroup.get(group) ?? []) {
      if (item.field === 'statement_name' || item.field === 'payee') {
        nameField = item.field
        nameOperator = toNameOperator(item.operator)
        // A stored filter can carry a repeat the builder would never write
        // (one hand-written, or written directly without running through the seed).
        keywordGroups.push(distinctKeywords(item.value_texts))
      } else if (group === groups[0]) {
        facetItems.push(item)
      }
    }
  }

  return {
    nameField,
    nameOperator,
    keywordGroups: keywordGroups.length === 0 ? [[]] : keywordGroups,
    facets: fromFilterItems(facetItems, universe),
  }
}

/** Narrow a select's string back to a name operator. `matches` is a regular expression on the same field. */
export function toNameOperator(value: string): NameOperator {
  return value === 'is_exactly' || value === 'matches' ? value : 'contains'
}

const NAME_VERBS: Record<NameOperator, string> = {
  contains: 'contains',
  is_exactly: 'is exactly',
  matches: 'matches',
}

/**
 * A one-line rendering of the conditions. Id facets are counted rather than
 * listed, but no facet may be left out: a condition missing from the summary
 * reads as a rule firing more widely than it does. With `names`, an id facet
 * of up to three named ids is spelled out instead.
 */
export function describeConditions(
  draft: ConditionDraft,
  names?: ReadonlyMap<string, string>,
): string[] {
  const chips: string[] = []
  const label = draft.nameField === 'statement_name' ? 'Statement name' : 'Payee'
  const verb = NAME_VERBS[draft.nameOperator]
  // Patterns are alternatives, not a set that must all be present, so they
  // join with "or" — writing "+" would describe a rule that cannot fire.
  const join = draft.nameOperator === 'matches' ? ' or ' : ' + '
  for (const keywords of draft.keywordGroups) {
    if (keywords.length > 0) chips.push(`${label} ${verb} ${keywords.join(join)}`)
  }

  const facets = draft.facets
  chips.push(...idChip(facets.categories, 'Category', 'category', 'categories', names))
  if (facets.uncategorized) chips.push('Uncategorized')
  chips.push(...idChip(facets.tags, 'Tag', 'tag', 'tags', names))
  if (facets.hasTags !== null) chips.push(facets.hasTags ? 'Tagged' : 'Untagged')
  chips.push(...idChip(facets.accounts, 'Account', 'account', 'accounts', names))
  if (facets.flags.values.length > 0) chips.push(facets.flags.negated ? 'Not flagged' : 'Flagged')
  for (const text of facets.texts) chips.push(`Mentions ${text}`)
  if (facets.amount !== null) {
    const amount = describeAmount(facets.amount)
    if (amount !== null) chips.push(amount)
  }
  if (facets.date !== null && (facets.date.from !== null || facets.date.to !== null)) {
    chips.push(`Dated ${facets.date.from ?? 'any'} to ${facets.date.to ?? 'any'}`)
  }
  for (const [value, yes, no] of [
    [facets.isBillOrSubscription, 'Bill or subscription', 'Not a bill or subscription'],
    [facets.excludedFromReports, 'Out of reports', 'In reports'],
    [facets.excludedFromSpendingPlan, 'Out of the spending plan', 'In the spending plan'],
    [facets.isReviewed, 'Reviewed', 'Unreviewed'],
    [facets.isPending, 'Pending', 'Settled'],
    [facets.hasAttachment, 'Has attachments', 'No attachments'],
    [facets.missingReceipt, 'Missing a receipt', 'Not missing a receipt'],
  ] as const) {
    if (value !== null) chips.push(value ? yes : no)
  }
  return chips
}

function idChip(
  facet: { ids: readonly string[]; negated: boolean },
  label: string,
  one: string,
  many: string,
  names: ReadonlyMap<string, string> | undefined,
): string[] {
  const named = facet.ids.map((id) => names?.get(id))
  if (names && facet.ids.length > 0 && facet.ids.length <= 3 && named.every((name) => name)) {
    return [`${facet.negated ? 'Not ' : ''}${label}: ${named.join(', ')}`]
  }
  return countChip(facet.ids.length, one, many, facet.negated)
}

function countChip(count: number, one: string, many: string, negated: boolean): string[] {
  if (count === 0) return []
  const noun = count === 1 ? one : many
  return [negated ? `Not ${count} ${noun}` : `${count} ${noun}`]
}

function describeAmount(amount: AmountFacet): string | null {
  const direction = amount.direction ?? 'amount'
  const bound = (typed: string | null) => {
    const wire = amountBound(typed)
    return wire === null ? null : formatMoney(parseMoney(wire))
  }
  const min = bound(amount.min)
  const max = bound(amount.max)
  switch (amountOperator(amount)) {
    case 'between':
      if (min === null && max === null) return null
      return `${direction} between ${min ?? 'any'} and ${max ?? 'any'}`
    case 'greater_than':
      return min === null ? null : `${direction} over ${min}`
    case 'less_than':
      return max === null ? null : `${direction} under ${max}`
    case 'equals':
      return min === null ? null : `${direction} is ${min}`
  }
}

/** What one rule, or one previewed row, would change. The two exclusions are described separately because they move separately. */
export function describeActions(
  actions: RuleActions,
  names: ReadonlyMap<string, string>,
): string[] {
  const out: string[] = []
  if (actions.set_payee !== null) out.push(`Payee → ${actions.set_payee}`)
  if (actions.set_category_id !== null) {
    out.push(`Category → ${names.get(actions.set_category_id) ?? 'a category'}`)
  }
  for (const id of actions.add_tag_ids) out.push(`+ ${names.get(id) ?? 'tag'}`)
  if (actions.set_notes !== null) out.push(`Note → ${actions.set_notes}`)
  if (actions.set_excluded_from_spending_plan !== null) {
    out.push(
      actions.set_excluded_from_spending_plan
        ? 'Out of the spending plan'
        : 'Back in the spending plan',
    )
  }
  if (actions.set_excluded_from_reports !== null) {
    out.push(actions.set_excluded_from_reports ? 'Out of reports' : 'Back in reports')
  }
  if (actions.set_is_reviewed !== null) {
    out.push(actions.set_is_reviewed ? 'Reviewed' : 'Unreviewed')
  }
  return out
}
