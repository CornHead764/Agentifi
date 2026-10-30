import { describe, expect, it } from 'vitest'

import {
  ACCOUNT_TYPE_GROUPS,
  accountTypeLabel,
  accountTypeOptions,
  kindForAccountType,
} from './accountTypes'

/**
 * Every account type `importer/taxonomy.go`, `importer/csvimport/inference.go`
 * and `provider/simplefin.go` can produce.
 */
const IMPORTABLE_TYPES = [
  '401k', '403b', '529_plan', 'brokerage', 'cash', 'cash_management', 'cd', 'checking',
  'construction_loan', 'consumer_loan', 'credit_card', 'crypto', 'custodial', 'digital_cash',
  'home_equity_loan', 'hsa', 'hsa_investment', 'ira', 'keogh', 'life_insurance', 'line_of_credit', 'military_loan',
  'money_market', 'mortgage', 'other_asset', 'other_banking', 'other_credit',
  'other_investment', 'other_liability', 'other_loan', 'real_estate', 'roth_401k',
  'roth_ira', 'savings', 'sep_ira', 'simple_ira', 'student_loan', 'vehicle', 'vehicle_loan',
]

describe('an account type on screen', () => {
  it('reads as English, not as the wire vocabulary', () => {
    expect(accountTypeLabel('credit_card')).toBe('Credit Card')
    expect(accountTypeLabel('other_asset')).toBe('Other Asset')
    expect(accountTypeLabel('real_estate')).toBe('Real Estate')
  })

  it('keeps the capitalisation of names that are not words', () => {
    expect(accountTypeLabel('hsa')).toBe('HSA')
    expect(accountTypeLabel('roth_ira')).toBe('Roth IRA')
    expect(accountTypeLabel('cd')).toBe('CD')
  })

  it('title-cases a type the map has not caught up with', () => {
    // Better a plausible label than snake_case in front of the user.
    expect(accountTypeLabel('crypto_wallet')).toBe('Crypto Wallet')
  })

  it('covers every type the importers can produce', () => {
    for (const type of IMPORTABLE_TYPES) {
      expect(accountTypeLabel(type)).not.toContain('_')
      expect(accountTypeLabel(type)).not.toBe(type)
    }
  })

  // Title-casing gets these wrong in a way a reader notices: "401k" survives
  // unchanged, and "403b" reads as a typo.
  it('writes the retirement plans the way their forms are named', () => {
    expect(accountTypeLabel('401k')).toBe('401(k)')
    expect(accountTypeLabel('roth_401k')).toBe('Roth 401(k)')
    expect(accountTypeLabel('403b')).toBe('403(b)')
    expect(accountTypeLabel('529_plan')).toBe('529 Plan')
  })
})

describe('the picker offering every loan type it labels', () => {
  it('offers construction_loan as a loan', () => {
    expect(kindForAccountType('construction_loan')).toBe('loan')
  })

  it('offers other_credit alongside credit_card and line_of_credit', () => {
    expect(kindForAccountType('other_credit')).toBe('credit_card')
  })

  it('offers every type an import can produce', () => {
    // A type an import produces but the picker never offers cannot be chosen
    // when creating or correcting an account.
    const offered = new Set(ACCOUNT_TYPE_GROUPS.flatMap((one) => one.types.map((t) => t.type)))
    for (const type of IMPORTABLE_TYPES) {
      expect(offered.has(type), `the picker does not offer ${type}`).toBe(true)
    }
  })
})

describe('crypto and life insurance cash value', () => {
  it('are offered as investments', () => {
    expect(kindForAccountType('crypto')).toBe('investment')
    expect(kindForAccountType('life_insurance')).toBe('investment')
    const labels = accountTypeOptions().map((one) => one.label)
    expect(labels).toContain('Investments · Crypto')
    expect(labels).toContain('Investments · Life Insurance (Cash Value)')
  })
})

describe('the two sides of an HSA', () => {
  it('offers the debit card account as banking and the brokerage as an investment', () => {
    expect(kindForAccountType('hsa')).toBe('cash')
    expect(kindForAccountType('hsa_investment')).toBe('investment')
    const labels = accountTypeOptions().map((one) => one.label)
    expect(labels).toContain('Banking · HSA')
    expect(labels).toContain('Investments · HSA Investment')
  })
})

describe('accountTypeOptions', () => {
  it('labels each offered type with its group', () => {
    expect(accountTypeOptions()[0]).toEqual({ value: 'checking', label: 'Banking · Checking' })
  })

  it('leads with a held type the picker does not offer, so it stays selected', () => {
    const options = accountTypeOptions('pension_plan')
    expect(options[0]).toEqual({ value: 'pension_plan', label: 'Pension Plan' })
    expect(options.slice(1)).toEqual(accountTypeOptions())
  })

  it('adds nothing for a held type it offers', () => {
    expect(accountTypeOptions('checking')).toEqual(accountTypeOptions())
  })
})
