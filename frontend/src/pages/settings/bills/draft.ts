/**
 * What the connection form holds, and what the resource is sent. Only the
 * figure the chosen autopay kind uses is sent, and an omitted one is left out
 * rather than sent as null, because the resource reads a present key as an
 * answer.
 */

import type {
  AutopayRule,
  Bill,
  BillReminderSuggestion,
  BillSubaccount,
} from '@/lib/clients/bills'
import type { Series } from '@/lib/clients/upcoming'
import { formatDate, plural } from '@/lib/format'
import { describeMonths } from '@/lib/recurrence'
import { formatMoney, parseWholeNumber, type MoneyFormatter } from '@/lib/money'

/** The rule and the one figure its kind uses, as a write carries them. */
export interface AutopayFields {
  autopay_rule: AutopayRule
  autopay_days?: number
  autopay_day?: number
}

/**
 * The autopay rule as fields, or null while the figure it needs is not a day.
 * The unused figure is not cleared here: the resource clears it on every write.
 */
export function autopayFields(kind: AutopayRule, figure: string): AutopayFields | null {
  const count = parseWholeNumber(figure)

  switch (kind) {
    case 'days_before_due':
      if (count === null || count > 60) return null
      return { autopay_rule: kind, autopay_days: count }
    case 'day_of_month':
      if (count === null || count < 1 || count > 31) return null
      return { autopay_rule: kind, autopay_day: count }
    default:
      return { autopay_rule: kind }
  }
}

/** The rule the autopay switch leaves: off is `none`, on starts at the due date. */
export function switchedAutopay(on: boolean): AutopayRule {
  return on ? 'on_due_date' : 'none'
}

/**
 * The open bill a household is next expected to pay, chosen by due date rather
 * than by the resource's list order. A superseded or paid bill is not an
 * answer.
 */
export function latestOpenBill(bills: readonly Bill[]): Bill | null {
  let best: Bill | null = null
  for (const bill of bills) {
    if (bill.status !== 'open') continue
    if (best === null || bill.due_on > best.due_on) best = bill
  }
  return best
}

/**
 * The accounts a card lists, and the hidden ones it folds away (the pull and
 * the reminder picker skip those). Order inside each group is the provider's.
 */
export function splitSubaccounts(subaccounts: readonly BillSubaccount[]): {
  shown: BillSubaccount[]
  hidden: BillSubaccount[]
} {
  return {
    shown: subaccounts.filter((one) => one.is_selected),
    hidden: subaccounts.filter((one) => !one.is_selected),
  }
}

/** One reminder a billed account can be pointed at, as its picker lists it. */
export interface ReminderChoice {
  id: string
  label: string
}

/**
 * The reminders a billed account can feed: running ones only, since an import
 * carries canceled twins under the same name that are easy to pick by mistake.
 * The one already linked stays whatever its state. A name that still appears
 * twice is told apart by amount and next due date. Income is never billed.
 */
export function reminderChoices(
  series: readonly Series[],
  linkedId: string | null,
  money: MoneyFormatter = formatMoney,
): ReminderChoice[] {
  const offered = series
    .filter((one) => one.kind !== 'income' && (one.is_active || one.id === linkedId))
    .sort((a, b) => a.label.localeCompare(b.label))
  const named = new Map<string, number>()
  for (const one of offered) named.set(one.label, (named.get(one.label) ?? 0) + 1)
  return offered.map((one) => {
    let label = one.label
    if ((named.get(one.label) ?? 0) > 1) {
      label += ` · ${money(one.amount, { signs: 'absolute' })}, due ${formatDate(one.due_on, 'short')}`
    }
    if (!one.is_active) label += ' (canceled)'
    return { id: one.id, label }
  })
}

/**
 * Said above a reminder suggested from bills: what it was read from, and what
 * the person should check before saving it.
 */
export function suggestionNotice(found: BillReminderSuggestion): string {
  const parts = [
    `Read from ${plural(found.due_dates, 'due date')}, ${formatDate(found.first_due, 'short')} to ${formatDate(found.last_due, 'short')}.`,
  ]
  if (found.recurrence.by_month.length > 0) {
    parts.push(`The bills come only in ${describeMonths(found.recurrence.by_month)}.`)
  }
  if (!found.confident) parts.push('They keep no exact rhythm, so check the schedule.')
  if (found.amount_varies) parts.push('The amount varies, and each bill sets its own.')
  parts.push(
    found.payment_ids.length > 0
      ? `${plural(found.payment_ids.length, 'payment')} in the bank history set the account and the matching text.`
      : 'No payment of these bills was found in the bank history, so choose the account it is paid from.',
  )
  return parts.join(' ')
}
