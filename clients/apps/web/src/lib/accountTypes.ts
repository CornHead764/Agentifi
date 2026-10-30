import { capitalize } from '@/lib/format'
import type { AccountKind, AccountWithBalances } from '@/lib/transactions/types'

/**
 * Labels for the stored type vocabulary, which round-trips Simplifi's export
 * and SimpleFIN unchanged. Anything unlisted is title-cased.
 */
const LABELS: Record<string, string> = {
  '401k': '401(k)',
  '403b': '403(b)',
  '529_plan': '529 Plan',
  brokerage: 'Brokerage',
  cash: 'Cash',
  cash_management: 'Cash Management',
  cd: 'CD',
  checking: 'Checking',
  construction_loan: 'Construction Loan',
  consumer_loan: 'Consumer Loan',
  credit_card: 'Credit Card',
  crypto: 'Crypto',
  custodial: 'Custodial',
  digital_cash: 'Digital Cash',
  gift_card: 'Gift Card',
  home_equity_loan: 'Home Equity Loan',
  hsa: 'HSA',
  hsa_investment: 'HSA Investment',
  ira: 'IRA',
  keogh: 'Keogh',
  life_insurance: 'Life Insurance (Cash Value)',
  line_of_credit: 'Line of Credit',
  military_loan: 'Military Loan',
  money_market: 'Money Market',
  mortgage: 'Mortgage',
  other_asset: 'Other Asset',
  other_banking: 'Other Banking',
  other_credit: 'Other Credit',
  other_investment: 'Other Investment',
  other_liability: 'Other Liability',
  other_loan: 'Other Loan',
  real_estate: 'Real Estate',
  roth_401k: 'Roth 401(k)',
  roth_ira: 'Roth IRA',
  savings: 'Savings',
  sep_ira: 'SEP IRA',
  simple_ira: 'SIMPLE IRA',
  student_loan: 'Student Loan',
  vehicle: 'Vehicle',
  vehicle_loan: 'Vehicle Loan',
}

export function accountTypeLabel(type: string): string {
  const known = LABELS[type]
  if (known) return known
  return type
    .split('_')
    .filter(Boolean)
    .map(capitalize)
    .join(' ')
}

/** Labels for `account.valuation_source` slugs; an unknown slug is shown verbatim. */
const VALUATION_SOURCE_LABELS: Record<string, string> = {
  zillow: 'Zillow',
  kbb: 'Kelley Blue Book',
}

export function valuationSourceLabel(source: string): string {
  return VALUATION_SOURCE_LABELS[source] ?? source
}

/**
 * The picker's types, with the kind each implies. Both are stored: `kind`
 * drives the arithmetic, `type` the label and drawer group. An imported
 * account may carry a type not offered here.
 */
export const ACCOUNT_TYPE_GROUPS: readonly {
  group: string
  types: readonly { type: string; kind: AccountKind }[]
}[] = [
  {
    group: 'Banking',
    types: [
      { type: 'checking', kind: 'cash' },
      { type: 'savings', kind: 'cash' },
      { type: 'money_market', kind: 'cash' },
      { type: 'cd', kind: 'cash' },
      { type: 'cash', kind: 'cash' },
      { type: 'cash_management', kind: 'cash' },
      { type: 'digital_cash', kind: 'cash' },
      { type: 'hsa', kind: 'cash' },
      { type: 'other_banking', kind: 'cash' },
      { type: 'credit_card', kind: 'credit_card' },
      { type: 'line_of_credit', kind: 'credit_card' },
      { type: 'other_credit', kind: 'credit_card' },
    ],
  },
  {
    group: 'Investments',
    types: [
      { type: 'brokerage', kind: 'investment' },
      { type: '401k', kind: 'investment' },
      { type: 'roth_401k', kind: 'investment' },
      { type: '403b', kind: 'investment' },
      { type: 'ira', kind: 'investment' },
      { type: 'roth_ira', kind: 'investment' },
      { type: 'sep_ira', kind: 'investment' },
      { type: 'simple_ira', kind: 'investment' },
      { type: '529_plan', kind: 'investment' },
      { type: 'hsa_investment', kind: 'investment' },
      { type: 'keogh', kind: 'investment' },
      { type: 'custodial', kind: 'investment' },
      { type: 'crypto', kind: 'investment' },
      { type: 'life_insurance', kind: 'investment' },
      { type: 'other_investment', kind: 'investment' },
    ],
  },
  {
    group: 'Assets',
    types: [
      { type: 'real_estate', kind: 'asset' },
      { type: 'vehicle', kind: 'asset' },
      { type: 'other_asset', kind: 'asset' },
    ],
  },
  {
    group: 'Liabilities',
    types: [
      { type: 'mortgage', kind: 'loan' },
      { type: 'home_equity_loan', kind: 'loan' },
      { type: 'vehicle_loan', kind: 'loan' },
      { type: 'student_loan', kind: 'loan' },
      { type: 'consumer_loan', kind: 'loan' },
      { type: 'military_loan', kind: 'loan' },
      { type: 'construction_loan', kind: 'loan' },
      { type: 'other_loan', kind: 'loan' },
      { type: 'other_liability', kind: 'loan' },
    ],
  },
]

const KIND_BY_TYPE = new Map(
  ACCOUNT_TYPE_GROUPS.flatMap((one) => one.types).map((one) => [one.type, one.kind]),
)

/** `undefined` for a type the picker does not offer: leave the stored kind alone. */
export function kindForAccountType(type: string): AccountKind | undefined {
  return KIND_BY_TYPE.get(type)
}

/**
 * The picker's choices. A `held` type the picker does not offer leads them, so
 * it still shows as selected.
 */
export function accountTypeOptions(held?: string): { value: string; label: string }[] {
  const options = ACCOUNT_TYPE_GROUPS.flatMap((one) =>
    one.types.map((entry) => ({
      value: entry.type,
      label: `${one.group} · ${accountTypeLabel(entry.type)}`,
    })),
  )
  if (held === undefined || kindForAccountType(held) !== undefined) return options
  return [{ value: held, label: accountTypeLabel(held) }, ...options]
}

/**
 * The kind that gates fields like "Secured on" for the chosen type. While
 * `type` is still the account's stored one, the stored `kind` stands in, since
 * an imported loan often carries a type the picker does not know.
 */
export function effectiveKind(
  type: string,
  account: Pick<AccountWithBalances, 'type' | 'kind'>,
): AccountKind | undefined {
  return kindForAccountType(type) ?? (type === account.type ? account.kind : undefined)
}
