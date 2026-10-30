/**
 * The projection is tested in `planning_test.go`. Here: the percent-to-fraction
 * conversion without float division, the typed dollar figures on the way out,
 * and the `MoneyShape` on the way in.
 */

import { describe, expect, it } from 'vitest'

import { coerceMoney } from '@/lib/api'
import { moneyFromCents } from '@/lib/money'

import {
  EMPTY_ADVANCED_INPUTS,
  EMPTY_RETIREMENT_INPUTS,
  RETIREMENT_SHAPE,
  advancedRetirementQuery,
  percentToFraction,
  retirementQuery,
  type RetirementProjection,
} from './planning'

describe('percentToFraction', () => {
  it('shifts the decimal point instead of dividing', () => {
    // Number('8.4') / 100 is 0.08399999999999999 and Number('2.9') / 100 is
    // 0.028999999999999998.
    expect(percentToFraction('8.4')).toBe('0.084')
    expect(percentToFraction('2.9')).toBe('0.029')
    expect(percentToFraction('7')).toBe('0.07')
    expect(percentToFraction('2.5')).toBe('0.025')
    expect(percentToFraction('12.75')).toBe('0.1275')
    expect(percentToFraction('100')).toBe('1.00')
    expect(percentToFraction('0')).toBe('0.00')
    expect(percentToFraction('-1.5')).toBe('-0.015')
  })

  it('leaves an unfinished entry empty so the default stands', () => {
    // A half-typed field must not become an assumption of zero return.
    for (const partial of ['', ' ', '-', '.', 'seven', '7%']) {
      expect(percentToFraction(partial)).toBe('')
    }
  })
})

describe('the dollar figures in the query', () => {
  it('sends amounts typed with a dollar sign or thousands commas as plain amounts', () => {
    const basic = new URLSearchParams(
      retirementQuery({
        ...EMPTY_RETIREMENT_INPUTS,
        currentBalance: '$1,250.50',
        monthlyContribution: '1,000',
      }) ?? '',
    )
    expect(basic.get('current_balance')).toBe('1250.50')
    expect(basic.get('monthly_contribution')).toBe('1000.00')
    expect(basic.has('annual_living_expenses')).toBe(false)

    const advanced = new URLSearchParams(
      advancedRetirementQuery({
        ...EMPTY_ADVANCED_INPUTS,
        taxableBalance: '$1,250.50',
        annualDeferredContribution: '1,000',
      }) ?? '',
    )
    expect(advanced.get('mode')).toBe('advanced')
    expect(advanced.get('taxable_balance')).toBe('1250.50')
    expect(advanced.get('annual_deferred_contribution')).toBe('1000.00')
  })

  it('is null while a dollar figure is not an amount, so nothing is fetched', () => {
    expect(retirementQuery({ ...EMPTY_RETIREMENT_INPUTS, currentBalance: 'lots' })).toBeNull()
    expect(
      advancedRetirementQuery({ ...EMPTY_ADVANCED_INPUTS, deferredBalance: 'lots' }),
    ).toBeNull()
  })
})

describe('the money shape', () => {
  it('reaches the amounts inside the years and the assumptions', () => {
    const body = {
      assumptions: {
        start_year: 2026,
        current_age: 35,
        retirement_age: 65,
        current_balance: '14000.00',
        is_balance_from_accounts: true,
        monthly_contribution: '500.00',
        annual_return: '0.07',
        annual_inflation: '0.025',
        withdrawal_rate: '0.04',
        target_annual_income: null,
        life_expectancy: 85,
        annual_living_expenses: '50000.00',
        annual_retirement_income: '20000.00',
        pre_retirement_tax_rate: '0.22',
        post_retirement_tax_rate: '0.22',
        return_spread: '0.02',
        advanced: {
          taxable_balance: '4000.00',
          deferred_balance: '10000.00',
          is_balance_from_accounts: true,
          annual_taxable_contribution: '5000.00',
          annual_deferred_contribution: '10000.00',
          contribution_growth: '0.03',
          post_retirement_return: '0.04',
        },
      },
      years: [
        {
          year: 2026,
          age: 35,
          balance: '14000.00',
          balance_in_todays_dollars: '14000.00',
          contributed: '0.00',
          drawn: '0.00',
          growth: '0.00',
          high_balance: '14100.00',
          low_balance: '13900.00',
          high_balance_in_todays_dollars: '14100.00',
          low_balance_in_todays_dollars: '13900.00',
        },
      ],
      years_to_retirement: 30,
      balance_at_retirement: '723616.46',
      balance_at_retirement_in_todays_dollars: '345098.71',
      total_contributed: '180000.00',
      total_growth: '529616.46',
      annual_income: '28944.66',
      annual_income_in_todays_dollars: '13803.95',
      meets_target: null,
      shortfall: null,
      runs_out_at_age: null,
    }

    const parsed = coerceMoney<RetirementProjection>(body, RETIREMENT_SHAPE)

    expect(parsed.balance_at_retirement).toBe(moneyFromCents(72_361_646))
    expect(parsed.assumptions.current_balance).toBe(moneyFromCents(1_400_000))
    expect(parsed.years[0].balance).toBe(moneyFromCents(1_400_000))
    expect(parsed.years[0].high_balance).toBe(moneyFromCents(1_410_000))
    expect(parsed.assumptions.annual_living_expenses).toBe(moneyFromCents(5_000_000))
    expect(parsed.assumptions.advanced?.deferred_balance).toBe(moneyFromCents(1_000_000))
    // A null stays null: no target is not a target of zero.
    expect(parsed.assumptions.target_annual_income).toBeNull()
    expect(parsed.shortfall).toBeNull()
  })
})
