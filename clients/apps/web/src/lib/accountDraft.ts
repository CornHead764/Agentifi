/**
 * The account details form as text fields, and the patch it saves. The patch
 * is a difference against the account the draft was taken from: a field left
 * alone is not sent, so a draft diffed against any other account would write
 * the first account's details into the second.
 */

import { kindForAccountType, effectiveKind } from '@/lib/accountTypes'
import type { AccountCreate, AccountSettingsPatch } from '@/lib/clients/connections'
import { blankToNull, rateAsPercentText } from '@/lib/format'
import {
  type Money,
  ZERO_MONEY,
  amountToWire,
  optionalAmountWire,
  parseAmountInput,
  parseWholeNumber,
} from '@/lib/money'
import { openingTab } from '@/lib/transactions/registerTab'
import type { AccountWithBalances, RegisterTab } from '@/lib/transactions/types'

/** A sentinel, because Radix's Select treats "" as no selection and draws a blank trigger. */
export const UNSECURED = 'none'

/** What a threshold field offers before anybody has typed one. */
export const SUGGESTED_THRESHOLD = '1.00'

/**
 * The account's own small-balance setting. `inherit` sends null and follows
 * the institution; `always` sends zero, which shows the account whatever its
 * institution says; `below` sends the typed amount.
 */
export type HideMode = 'inherit' | 'always' | 'below'

export function asHideMode(value: string): HideMode {
  return value === 'always' || value === 'below' ? value : 'inherit'
}

function hideModeOf(threshold: Money | null): HideMode {
  if (threshold === null) return 'inherit'
  return threshold === ZERO_MONEY ? 'always' : 'below'
}

export interface AccountDraft {
  name: string
  notes: string
  type: string
  currency: string
  masked: string
  opening: string
  openingOn: string
  historyStart: string
  limit: string
  closeDay: string
  statement: string
  minimum: string
  dueDate: string
  apr: string
  excludePending: boolean
  requiresReceipts: boolean
  securedBy: string
  logo: string
  address: string
  vin: string
  mileage: string
  mileageOn: string
  perYear: string
  closed: boolean
  hideMode: HideMode
  hideBelow: string
  openingTab: RegisterTab
}

export function draftOf(account: AccountWithBalances): AccountDraft {
  return {
    name: account.name,
    notes: account.notes ?? '',
    type: account.type,
    currency: account.currency,
    masked: account.masked_number ?? '',
    opening: amountToWire(account.opening_balance),
    openingOn: account.opening_balance_on ?? '',
    historyStart: account.history_starts_on ?? '',
    limit: wireOrBlank(account.credit_limit),
    closeDay: numberText(account.statement_close_day),
    statement: wireOrBlank(account.statement_balance),
    minimum: wireOrBlank(account.minimum_due),
    dueDate: account.due_date ?? '',
    apr: rateAsPercentText(account.interest_rate),
    excludePending: account.exclude_bank_pending,
    requiresReceipts: account.requires_receipts,
    securedBy: account.secured_by_account_id ?? UNSECURED,
    logo: account.custom_logo_url ?? '',
    address: account.property_address ?? '',
    vin: account.vehicle_vin ?? '',
    mileage: numberText(account.vehicle_mileage),
    mileageOn: account.vehicle_mileage_as_of ?? '',
    perYear: numberText(account.vehicle_miles_per_year),
    closed: account.is_closed,
    hideMode: hideModeOf(account.hide_below_balance),
    hideBelow:
      account.hide_below_balance !== null && account.hide_below_balance !== ZERO_MONEY
        ? amountToWire(account.hide_below_balance)
        : SUGGESTED_THRESHOLD,
    openingTab: openingTab(account),
  }
}

/** The patch, or the first typed value that stops the save, as a toast title. */
export type DraftPatch = { patch: AccountSettingsPatch } | { problem: string }

/**
 * What saving the draft changes on `account`, the account it was taken from.
 * `canPrice` is whether the chosen type has lookup details (an address, a VIN).
 */
export function patchOf(
  draft: AccountDraft,
  account: AccountWithBalances,
  canPrice: boolean,
): DraftPatch {
  const patch: AccountSettingsPatch = {}
  const isLoan = effectiveKind(draft.type, account) === 'loan'
  const isCredit = effectiveKind(draft.type, account) === 'credit_card'

  // For fields where blank means "no such figure": a typo stops the save, and
  // an unedited figure is not sent at all.
  const amount = (typed: string, current: Money | null): string | null | undefined => {
    const text = typed.trim()
    if (text === '') return current === null ? undefined : null
    const parsed = parseAmountInput(text)
    if (!parsed) throw new Problem(`"${text}" is not an amount`)
    return current === null || parsed.wire !== amountToWire(current) ? parsed.wire : undefined
  }
  const text = (typed: string, current: string | null): string | null | undefined =>
    blankToNull(typed) === current ? undefined : blankToNull(typed)
  const whole = (typed: string, current: number | null): number | null | undefined =>
    numberOrNull(typed) === current ? undefined : numberOrNull(typed)
  const set = <K extends keyof AccountSettingsPatch>(field: K, value: AccountSettingsPatch[K]) => {
    if (value !== undefined) patch[field] = value
  }

  try {
    const trimmedName = draft.name.trim()
    if (trimmedName !== '' && trimmedName !== account.name) patch.name = trimmedName
    set('notes', text(draft.notes, account.notes))
    if (draft.closed !== account.is_closed) patch.is_closed = draft.closed
    if (draft.excludePending !== account.exclude_bank_pending) {
      patch.exclude_bank_pending = draft.excludePending
    }
    if (draft.requiresReceipts !== account.requires_receipts) {
      patch.requires_receipts = draft.requiresReceipts
    }
    if (draft.currency !== account.currency) patch.currency = draft.currency
    set('masked_number', text(draft.masked, account.masked_number))
    set('custom_logo_url', text(draft.logo, account.custom_logo_url))

    // The opening balance anchors every running balance, so it is sent only
    // when edited, and blank leaves it alone rather than meaning zero.
    const typedOpening = draft.opening.trim()
    if (typedOpening !== '' && typedOpening !== amountToWire(account.opening_balance)) {
      const parsed = parseAmountInput(typedOpening)
      if (!parsed) throw new Problem(`"${typedOpening}" is not an amount`)
      patch.opening_balance = parsed.wire
    }
    set('opening_balance_on', text(draft.openingOn, account.opening_balance_on))
    set('history_starts_on', text(draft.historyStart, account.history_starts_on))

    if (isCredit) {
      // Never a float: the typed text is read as digits and sent as the 2dp
      // string the server parses as a Decimal.
      set('credit_limit', amount(draft.limit, account.credit_limit))
      const day = numberOrNull(draft.closeDay)
      if (day !== null && (day < 1 || day > 31)) {
        throw new Problem('The statement closes on a day between 1 and 31')
      }
      if (day !== account.statement_close_day) patch.statement_close_day = day

      // Blank clears the field, so the register header leaves it out rather
      // than drawing a dash.
      set('statement_balance', amount(draft.statement, account.statement_balance))
      set('minimum_due', amount(draft.minimum, account.minimum_due))
      set('due_date', text(draft.dueDate, account.due_date))
      const typedApr = draft.apr.trim()
      if (typedApr === '') {
        if (account.interest_rate !== null) patch.interest_rate = null
      } else if (typedApr !== rateAsPercentText(account.interest_rate)) {
        // As typed: the server reads 24.99 and 0.2499 as the same rate, so the
        // percentage never passes through a float on its way to a Decimal.
        patch.interest_rate = typedApr
      }
    }
    if (draft.type !== account.type) {
      patch.type = draft.type
      // An imported type can be outside the picker's list; forcing its kind to
      // a default would silently re-sign its balance.
      const kind = kindForAccountType(draft.type)
      if (kind) patch.kind = kind
    }
    if (isLoan) {
      const securedBy = draft.securedBy === UNSECURED ? null : draft.securedBy
      if (securedBy !== account.secured_by_account_id) patch.secured_by_account_id = securedBy
    }

    let hideAt: string | null = null
    if (draft.hideMode === 'always') hideAt = amountToWire(ZERO_MONEY)
    if (draft.hideMode === 'below') hideAt = thresholdOrProblem(draft.hideBelow)
    if (hideAt !== wireOrNull(account.hide_below_balance)) patch.hide_below_balance = hideAt

    // The rows are where a register opens anyway, so choosing them clears the
    // setting rather than storing it.
    if (draft.openingTab !== openingTab(account)) {
      patch.default_register_tab = draft.openingTab === 'all' ? null : draft.openingTab
    }

    if (canPrice) {
      set('property_address', text(draft.address, account.property_address))
      set('vehicle_vin', text(draft.vin, account.vehicle_vin))
      set('vehicle_mileage', whole(draft.mileage, account.vehicle_mileage))
      set('vehicle_mileage_as_of', text(draft.mileageOn, account.vehicle_mileage_as_of))
      set('vehicle_miles_per_year', whole(draft.perYear, account.vehicle_miles_per_year))
    }
  } catch (error) {
    if (error instanceof Problem) return { problem: error.message }
    throw error
  }
  return { patch }
}

class Problem extends Error {}

/**
 * A typed small-balance threshold as a wire amount. A threshold is a
 * magnitude, so a minus sign is a typo rather than a rule; null is a typo.
 */
export function thresholdWire(typed: string): string | null {
  const parsed = parseAmountInput(typed)
  return !parsed || parsed.wire.startsWith('-') ? null : parsed.wire
}

export function thresholdProblem(typed: string): string {
  return `"${typed.trim()}" is not an amount to hide under`
}

function thresholdOrProblem(typed: string): string {
  const wire = thresholdWire(typed)
  if (wire === null) throw new Problem(thresholdProblem(typed))
  return wire
}

export function wireOrNull(value: Money | null): string | null {
  return value === null ? null : amountToWire(value)
}

function wireOrBlank(value: Money | null): string {
  return value === null ? '' : amountToWire(value)
}

/** Blank is "no such figure"; anything else must be a whole number. */
function numberOrNull(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = parseWholeNumber(trimmed)
  if (parsed === null) throw new Problem(`"${trimmed}" is not a whole number`)
  return parsed
}

/** Zero is a real reading, so only an absent value renders blank. */
function numberText(value: number | null): string {
  return value === null ? '' : String(value)
}

/** The new-account form as typed. */
export interface NewAccountFields {
  name: string
  type: string
  currency: string
  balance: string
  address: string
  vin: string
}

/** The body, or `null` without a name or with a starting balance that is not an amount. */
export function newAccountBody(fields: NewAccountFields): AccountCreate | null {
  const name = fields.name.trim()
  const balance = optionalAmountWire(fields.balance)
  if (name === '' || balance === undefined) return null
  const address = fields.address.trim()
  const vin = fields.vin.trim()
  return {
    name,
    kind: kindForAccountType(fields.type) ?? 'cash',
    type: fields.type,
    opening_balance: balance ?? undefined,
    currency: fields.currency,
    property_address: fields.type === 'real_estate' && address !== '' ? address : undefined,
    vehicle_vin: fields.type === 'vehicle' && vin !== '' ? vin : undefined,
  }
}
