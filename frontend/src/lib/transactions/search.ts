/**
 * The search DSL, parsed into the one `Filter`. Space-separated terms AND. The
 * grammar is Simplifi's "Shortcuts" cheat sheet and nothing outside it is
 * invented. A term the filter cannot express goes in `limitations` rather than
 * being dropped or widened in silence.
 */

import { resolveDatePreset } from '@/lib/dateRanges'
import { parseIsoDay } from '@/lib/format'
import {
  absMoney,
  addMoney,
  amountToWire,
  parseAmountInput,
  scaleMoney,
  subMoney,
} from '@/lib/money'

import { EMPTY_DRAFT, EMPTY_UNIVERSE, type FilterDraft, type FilterUniverse } from './filter'
import type { Uuid } from './types'

export interface ParsedSearch {
  draft: FilterDraft
  /** Terms understood but not expressible, each already phrased for the user. */
  limitations: string[]
  /** Terms that are not the grammar at all. */
  errors: string[]
}

export interface ParseOptions {
  universe?: FilterUniverse
  /** Injected so a relative window is testable. */
  today?: Date
}

const KEYED = /^([a-zA-Z]+)(!=|>=|<=|:|=|>|<)(.*)$/
const APPROX = /^(.+?)(?:±|\+-)(\d+(?:\.\d+)?)%$/

function isIsoDay(value: string): boolean {
  return parseIsoDay(value) !== null
}

export function parseSearch(query: string, options: ParseOptions = {}): ParsedSearch {
  const universe = options.universe ?? EMPTY_UNIVERSE
  const today = options.today ?? new Date()

  const draft: FilterDraft = {
    ...EMPTY_DRAFT,
    categories: { ids: [], negated: false },
    payees: { values: [], negated: false },
    tags: { ids: [], negated: false },
    accounts: { ids: [], negated: false },
    flags: { values: [], negated: false },
    texts: [],
  }
  const limitations: string[] = []
  const errors: string[] = []

  for (const token of tokenize(query)) {
    const keyed = KEYED.exec(token)
    if (!keyed) {
      const text = unquote(token)
      if (text) draft.texts.push(text)
      continue
    }

    const [, rawKey, operator, rawValue] = keyed
    const key = rawKey.toLowerCase()
    const value = unquote(rawValue)
    const negated = operator === '!='

    switch (key) {
      case 'payee':
        if (value) {
          // The cheat sheet says "Payee contains": each value is a substring test.
          draft.payees = { values: splitList(value), negated, operator: 'contains' }
        }
        break

      case 'category': {
        const resolved = resolveNames(splitList(value), universe.categories, 'category', errors)
        if (resolved.length > 0) draft.categories = { ids: resolved, negated }
        break
      }

      case 'tag':
      case 'tags': {
        const resolved = resolveNames(splitList(value), universe.tags, 'tag', errors)
        if (resolved.length > 0) draft.tags = { ids: resolved, negated }
        break
      }

      case 'date':
        applyDate(draft, operator, value, today, errors)
        break

      case 'amount':
        applyAmount(draft, operator, value, errors)
        break

      case 'expense':
        applyAmount(draft, operator, value, errors)
        // Only once the size parsed, so a malformed term leaves no bare sign test.
        if (draft.amount !== null) draft.amount = { ...draft.amount, direction: 'expense' }
        break

      case 'is':
      case 'not':
        applyState(draft, key === 'is', value, errors)
        break

      default:
        errors.push(`\`${token}\` is not a search term.`)
    }
  }

  return { draft, limitations: [...new Set(limitations)], errors }
}

/** Split on whitespace, treating a quoted run as one token and keeping its quotes. */
function tokenize(query: string): string[] {
  const tokens: string[] = []
  let current = ''
  let quoted = false

  for (const character of query) {
    if (character === '"') {
      quoted = !quoted
      current += character
      continue
    }
    if (!quoted && /\s/.test(character)) {
      if (current) tokens.push(current)
      current = ''
      continue
    }
    current += character
  }
  if (current) tokens.push(current)
  return tokens
}

function unquote(value: string): string {
  const trimmed = value.trim()
  if (trimmed.length >= 2 && trimmed.startsWith('"') && trimmed.endsWith('"')) {
    return trimmed.slice(1, -1)
  }
  return trimmed.replace(/"/g, '')
}

/** `walmart,target` — a comma list is the OR inside one facet. */
function splitList(value: string): string[] {
  return value
    .split(',')
    .map((entry) => unquote(entry))
    .filter((entry) => entry.length > 0)
}

function resolveNames(
  names: readonly string[],
  rows: readonly { id: Uuid; name: string }[],
  label: string,
  errors: string[],
): Uuid[] {
  // Nothing loaded yet is not a bad query; the caller re-parses when it is.
  if (rows.length === 0) return []

  const byName = new Map(rows.map((row) => [row.name.toLowerCase(), row.id]))
  const ids: Uuid[] = []
  for (const name of names) {
    const id = byName.get(name.toLowerCase())
    if (id === undefined) errors.push(`No ${label} named "${name}".`)
    else ids.push(id)
  }
  return ids
}

function applyDate(
  draft: FilterDraft,
  operator: string,
  value: string,
  today: Date,
  errors: string[],
): void {
  const range = value.split('..')
  if (range.length === 2 && isIsoDay(range[0]) && isIsoDay(range[1])) {
    draft.date = { from: range[0], to: range[1], preset: null }
    return
  }

  if (isIsoDay(value)) {
    if (operator === '>=' || operator === '>') draft.date = { from: value, to: null, preset: null }
    else if (operator === '<=' || operator === '<') {
      draft.date = { from: null, to: value, preset: null }
    } else draft.date = { from: value, to: value, preset: null }
    return
  }

  const preset = resolveDatePreset(value, today)
  if (preset === null) {
    errors.push(`\`date${operator}${value}\` is not a date or a period.`)
    return
  }
  draft.date = { from: preset.from, to: preset.to, preset: value }
}

function applyAmount(
  draft: FilterDraft,
  operator: string,
  value: string,
  errors: string[],
): void {
  const approx = APPROX.exec(value)
  if (approx) {
    const centre = parseAmountInput(approx[1])
    if (centre === null) {
      errors.push(`\`${value}\` is not an amount.`)
      return
    }
    // Integer cents throughout, so a tolerance never passes through a float.
    const tolerance = absMoney(scaleMoney(centre.cents, Number(approx[2]) / 100))
    draft.amount = {
      min: amountToWire(subMoney(centre.cents, tolerance)),
      max: amountToWire(addMoney(centre.cents, tolerance)),
    }
    return
  }

  const range = value.split('..')
  if (range.length === 2) {
    const low = parseAmountInput(range[0])
    const high = parseAmountInput(range[1])
    if (low === null || high === null) {
      errors.push(`\`${value}\` is not an amount range.`)
      return
    }
    draft.amount = { min: low.wire, max: high.wire }
    return
  }

  const parsed = parseAmountInput(value)
  if (parsed === null) {
    errors.push(`\`${value}\` is not an amount.`)
    return
  }
  // The range test is inclusive at both ends, so `>` and `>=` store the same.
  if (operator === '>' || operator === '>=') draft.amount = { min: parsed.wire, max: null }
  else if (operator === '<' || operator === '<=') draft.amount = { min: null, max: parsed.wire }
  else draft.amount = { min: parsed.wire, max: parsed.wire }
}

function applyState(
  draft: FilterDraft,
  affirmative: boolean,
  value: string,
  errors: string[],
): void {
  switch (value.toLowerCase()) {
    case 'reviewed':
      draft.isReviewed = affirmative
      return
    case 'uncategorized':
      draft.uncategorized = affirmative
      return
    case 'undetermined':
      draft.categoryUndetermined = affirmative
      return
    case 'suggested':
      draft.hasCategorySuggestion = affirmative
      return
    case 'tags':
      draft.hasTags = affirmative
      return
    case 'pending':
      draft.isPending = affirmative
      return
    case 'attached':
      draft.hasAttachment = affirmative
      return
    case 'missing-receipt':
      draft.missingReceipt = affirmative
      return
    default:
      errors.push(`\`${affirmative ? 'is' : 'not'}:${value}\` is not a search term.`)
  }
}
