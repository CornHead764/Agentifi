/**
 * The retirement projection. The server echoes every assumption it used,
 * defaults included, and the page renders those rather than its own form
 * state. Rates on the wire are fractions (`0.07` is 7%).
 */

import { useQuery } from '@tanstack/react-query'

import { api, type MoneyShape } from '@/lib/api'
import { optionalAmountWire, type Money } from '@/lib/money'

import { queryString, type WireRate } from './entities'

const planningKeys = {
  retirement: (query: string | null) => ['planning', 'retirement', query] as const,
}

export interface RetirementAssumptions {
  start_year: number
  current_age: number
  retirement_age: number
  current_balance: Money
  /** False when the caller overrode the figure the accounts gave. */
  is_balance_from_accounts: boolean
  monthly_contribution: Money
  /** Fractions: `0.07` is 7%. */
  annual_return: WireRate
  annual_inflation: WireRate
  withdrawal_rate: WireRate
  /** Null when no target was stated, which is not a target of zero. */
  target_annual_income: Money | null
  life_expectancy: number
  annual_living_expenses: Money
  annual_retirement_income: Money
  pre_retirement_tax_rate: WireRate
  post_retirement_tax_rate: WireRate
  /** The ± band the high and low estimates walk. */
  return_spread: WireRate
  /** Null for a basic projection. */
  advanced: AdvancedAssumptions | null
}

export interface AdvancedAssumptions {
  taxable_balance: Money
  deferred_balance: Money
  is_balance_from_accounts: boolean
  annual_taxable_contribution: Money
  annual_deferred_contribution: Money
  contribution_growth: WireRate
  post_retirement_return: WireRate
}

export interface RetirementYear {
  year: number
  age: number
  /** End of that year, in that year's dollars. */
  balance: Money
  balance_in_todays_dollars: Money
  /** Cumulative since the projection opened. */
  contributed: Money
  drawn: Money
  growth: Money
  /** The same walk at return ± spread. */
  high_balance: Money
  low_balance: Money
  high_balance_in_todays_dollars: Money
  low_balance_in_todays_dollars: Money
}

export interface RetirementProjection {
  assumptions: RetirementAssumptions
  years: RetirementYear[]
  /** Zero once the retirement age has been reached; `years` is then one entry. */
  years_to_retirement: number
  balance_at_retirement: Money
  balance_at_retirement_in_todays_dollars: Money
  total_contributed: Money
  total_growth: Money
  annual_income: Money
  annual_income_in_todays_dollars: Money
  /** Both null until a target is stated: no target means no verdict. */
  meets_target: boolean | null
  shortfall: Money | null
  /** Null while the money lasts. */
  runs_out_at_age: number | null
}

export const RETIREMENT_SHAPE: MoneyShape<RetirementProjection> = {
  assumptions: {
    current_balance: 'money',
    monthly_contribution: 'money',
    target_annual_income: 'money',
    annual_living_expenses: 'money',
    annual_retirement_income: 'money',
    advanced: {
      taxable_balance: 'money',
      deferred_balance: 'money',
      annual_taxable_contribution: 'money',
      annual_deferred_contribution: 'money',
    },
  },
  years: {
    balance: 'money',
    balance_in_todays_dollars: 'money',
    contributed: 'money',
    drawn: 'money',
    growth: 'money',
    high_balance: 'money',
    low_balance: 'money',
    high_balance_in_todays_dollars: 'money',
    low_balance_in_todays_dollars: 'money',
  },
  balance_at_retirement: 'money',
  balance_at_retirement_in_todays_dollars: 'money',
  total_contributed: 'money',
  total_growth: 'money',
  annual_income: 'money',
  annual_income_in_todays_dollars: 'money',
  shortfall: 'money',
}

/**
 * Raw strings as typed, never numbers. Empty means "not stated": the parameter
 * is dropped and the server's default is used and echoed back.
 */
export interface RetirementInputs {
  currentAge: string
  retirementAge: string
  /** Percent units, converted on the way out. */
  annualReturnPercent: string
  annualInflationPercent: string
  withdrawalRatePercent: string
  monthlyContribution: string
  targetAnnualIncome: string
  /** Empty uses the investment accounts' total. */
  currentBalance: string
  lifeExpectancy: string
  annualLivingExpenses: string
  annualRetirementIncome: string
  preRetirementTaxPercent: string
  postRetirementTaxPercent: string
}

/** Blank on purpose: `planning.go` alone owns the defaults. */
export const EMPTY_RETIREMENT_INPUTS: RetirementInputs = {
  currentAge: '',
  retirementAge: '',
  annualReturnPercent: '',
  annualInflationPercent: '',
  withdrawalRatePercent: '',
  monthlyContribution: '',
  targetAnnualIncome: '',
  currentBalance: '',
  lifeExpectancy: '',
  annualLivingExpenses: '',
  annualRetirementIncome: '',
  preRetirementTaxPercent: '',
  postRetirementTaxPercent: '',
}

/**
 * Moves the decimal point textually: `8.4 / 100` is `0.08399999999999999`.
 * Unparseable input comes back empty so the server's default stands.
 */
export function percentToFraction(percent: string): string {
  const trimmed = percent.trim()
  if (!/^-?\d*\.?\d*$/.test(trimmed) || trimmed === '' || trimmed === '-' || trimmed === '.') {
    return ''
  }
  const negative = trimmed.startsWith('-')
  const [whole = '', fraction = ''] = (negative ? trimmed.slice(1) : trimmed).split('.')
  const padded = (whole || '0').padStart(3, '0')
  const cut = padded.length - 2
  return `${negative ? '-' : ''}${padded.slice(0, cut)}.${padded.slice(cut)}${fraction}`
}

function stated(value: string): string | null {
  const trimmed = value.trim()
  return trimmed === '' ? null : trimmed
}

/**
 * The typed dollar figures as wire amounts, or `null` when any is not an
 * amount. Blank lets the server's default stand.
 */
function amounts(fields: Record<string, string>): Record<string, string | null> | null {
  const out: Record<string, string | null> = {}
  for (const [key, typed] of Object.entries(fields)) {
    const wire = optionalAmountWire(typed)
    if (wire === undefined) return null
    out[key] = wire
  }
  return out
}

/** The query string, or `null` while a dollar figure is not an amount. */
export function retirementQuery(inputs: RetirementInputs): string | null {
  const money = amounts({
    monthly_contribution: inputs.monthlyContribution,
    target_annual_income: inputs.targetAnnualIncome,
    current_balance: inputs.currentBalance,
    annual_living_expenses: inputs.annualLivingExpenses,
    annual_retirement_income: inputs.annualRetirementIncome,
  })
  if (money === null) return null
  return queryString({
    current_age: stated(inputs.currentAge),
    retirement_age: stated(inputs.retirementAge),
    annual_return: stated(percentToFraction(inputs.annualReturnPercent)),
    annual_inflation: stated(percentToFraction(inputs.annualInflationPercent)),
    withdrawal_rate: stated(percentToFraction(inputs.withdrawalRatePercent)),
    life_expectancy: stated(inputs.lifeExpectancy),
    ...money,
    pre_retirement_tax_rate: stated(percentToFraction(inputs.preRetirementTaxPercent)),
    post_retirement_tax_rate: stated(percentToFraction(inputs.postRetirementTaxPercent)),
  })
}

/**
 * While a dollar figure is not an amount nothing is fetched, and the last
 * projection stays on screen.
 */
export function useRetirementProjection(inputs: RetirementInputs, enabled = true) {
  const query = retirementQuery(inputs)
  return useQuery({
    queryKey: planningKeys.retirement(query),
    enabled: enabled && query !== null,
    queryFn: ({ signal }) =>
      api.get<RetirementProjection>(`/planning/retirement${query ?? ''}`, RETIREMENT_SHAPE, signal),
    placeholderData: (previous) => previous,
  })
}

/** Separate from the basic form: the two modes' inputs are independent. */
export interface AdvancedRetirementInputs {
  currentAge: string
  retirementAge: string
  lifeExpectancy: string
  taxableBalance: string
  deferredBalance: string
  annualTaxableContribution: string
  annualDeferredContribution: string
  contributionGrowthPercent: string
  annualLivingExpenses: string
  annualRetirementIncome: string
  targetAnnualIncome: string
  preReturnPercent: string
  postReturnPercent: string
  annualInflationPercent: string
  preRetirementTaxPercent: string
  postRetirementTaxPercent: string
  withdrawalRatePercent: string
  returnSpreadPercent: string
}

export const EMPTY_ADVANCED_INPUTS: AdvancedRetirementInputs = {
  currentAge: '',
  retirementAge: '',
  lifeExpectancy: '',
  taxableBalance: '',
  deferredBalance: '',
  annualTaxableContribution: '',
  annualDeferredContribution: '',
  contributionGrowthPercent: '',
  annualLivingExpenses: '',
  annualRetirementIncome: '',
  targetAnnualIncome: '',
  preReturnPercent: '',
  postReturnPercent: '',
  annualInflationPercent: '',
  preRetirementTaxPercent: '',
  postRetirementTaxPercent: '',
  withdrawalRatePercent: '',
  returnSpreadPercent: '',
}

/** The query string, or `null` while a dollar figure is not an amount. */
export function advancedRetirementQuery(inputs: AdvancedRetirementInputs): string | null {
  const money = amounts({
    taxable_balance: inputs.taxableBalance,
    deferred_balance: inputs.deferredBalance,
    annual_taxable_contribution: inputs.annualTaxableContribution,
    annual_deferred_contribution: inputs.annualDeferredContribution,
    annual_living_expenses: inputs.annualLivingExpenses,
    annual_retirement_income: inputs.annualRetirementIncome,
    target_annual_income: inputs.targetAnnualIncome,
  })
  if (money === null) return null
  return queryString({
    mode: 'advanced',
    current_age: stated(inputs.currentAge),
    retirement_age: stated(inputs.retirementAge),
    life_expectancy: stated(inputs.lifeExpectancy),
    contribution_growth: stated(percentToFraction(inputs.contributionGrowthPercent)),
    ...money,
    annual_return: stated(percentToFraction(inputs.preReturnPercent)),
    post_retirement_return: stated(percentToFraction(inputs.postReturnPercent)),
    annual_inflation: stated(percentToFraction(inputs.annualInflationPercent)),
    pre_retirement_tax_rate: stated(percentToFraction(inputs.preRetirementTaxPercent)),
    post_retirement_tax_rate: stated(percentToFraction(inputs.postRetirementTaxPercent)),
    withdrawal_rate: stated(percentToFraction(inputs.withdrawalRatePercent)),
    return_spread: stated(percentToFraction(inputs.returnSpreadPercent)),
  })
}

export function useAdvancedRetirementProjection(inputs: AdvancedRetirementInputs, enabled = true) {
  const query = advancedRetirementQuery(inputs)
  return useQuery({
    queryKey: planningKeys.retirement(query),
    enabled: enabled && query !== null,
    queryFn: ({ signal }) =>
      api.get<RetirementProjection>(`/planning/retirement${query ?? ''}`, RETIREMENT_SHAPE, signal),
    placeholderData: (previous) => previous,
  })
}
