import { describe, expect, it } from 'vitest'

import type { PricedAs, ValuationResult } from '@/lib/clients/connections'
import { parseMoney } from '@/lib/money'

import {
  canReprice,
  pricedAsText,
  repricingTitle,
  revaluationFailureTitle,
  revaluationReport,
  singleRevaluationReport,
  singleRevaluationResults,
} from './revaluation'

function result(overrides: Partial<ValuationResult> = {}): ValuationResult {
  return {
    account_id: 'a1',
    name: 'Car 1',
    skipped: null,
    source: 'kbb',
    estimate: null,
    adjustment: null,
    mileage_used: null,
    priced_as: null,
    ...overrides,
  }
}

const repriced = result({
  estimate: parseMoney('17500.00'),
  adjustment: parseMoney('-2500.00'),
})
const unchanged = result({
  name: 'House 1',
  estimate: parseMoney('300000.00'),
  skipped: 'the estimate matches what the ledger already says',
})
const noEstimate = result({
  name: 'Car 2',
  skipped: 'the source had no estimate for this asset',
})

function pricedAs(overrides: Partial<PricedAs> = {}): PricedAs {
  return {
    year: null,
    make: null,
    model: null,
    trim: null,
    mileage: null,
    typical_mileage: false,
    address: null,
    low: null,
    high: null,
    ...overrides,
  }
}

const roadster = pricedAs({
  year: '2019',
  make: 'Examplemotors',
  model: 'Roadster',
  trim: 'Sport',
  mileage: 41000,
  low: parseMoney('16100.00'),
  high: parseMoney('18900.00'),
})
const cottage = pricedAs({ address: '42 Example Way, Sampleton, ZZ 00000' })

describe('what the source priced', () => {
  it('names the car, its mileage and the range', () => {
    expect(pricedAsText(roadster, 'USD')).toBe(
      'Priced as a 2019 Examplemotors Roadster Sport at 41,000 miles. Range $16,100 to $18,900.',
    )
  })

  it('says when the mileage was the typical one rather than a reading', () => {
    const typical = pricedAs({
      year: '2011',
      make: 'Examplemotors',
      model: 'Wagon',
      mileage: 98000,
      typical_mileage: true,
    })
    expect(pricedAsText(typical, 'USD')).toBe(
      'Priced as a 2011 Examplemotors Wagon at 98,000 miles, a typical mileage, as no odometer reading is stored.',
    )
  })

  it('names the house it matched', () => {
    expect(pricedAsText(cottage, 'USD')).toBe('Matched to 42 Example Way, Sampleton, ZZ 00000.')
  })

  it('says nothing when the source said nothing', () => {
    expect(pricedAsText(null)).toBeNull()
    expect(pricedAsText(pricedAs())).toBeNull()
  })
})

describe('the re-price-all report', () => {
  it('counts what moved, what matched and what was skipped, and says why', () => {
    const report = revaluationReport([repriced, unchanged, noEstimate])
    expect(report.title).toBe('Re-priced 1 asset')
    expect(report.tone).toBe('success')
    expect(report.description).toBe(
      '1 re-priced, 1 unchanged, 1 skipped.\nCar 2: The source had no estimate for this asset.',
    )
  })

  it('says per asset what each was priced as', () => {
    const report = revaluationReport([
      { ...repriced, priced_as: roadster },
      { ...unchanged, priced_as: cottage },
      noEstimate,
    ])
    expect(report.description?.split('\n')).toEqual([
      '1 re-priced, 1 unchanged, 1 skipped.',
      'Car 1: Priced as a 2019 Examplemotors Roadster Sport at 41,000 miles. Range $16,100 to $18,900.',
      'House 1: Matched to 42 Example Way, Sampleton, ZZ 00000.',
      'Car 2: The source had no estimate for this asset.',
    ])
  })

  it('says plainly when nothing was re-priced', () => {
    const report = revaluationReport([unchanged, noEstimate])
    expect(report.title).toBe('Nothing was re-priced')
    expect(report.tone).toBe('neutral')
    expect(report.description).toContain('1 unchanged, 1 skipped.')
  })

  it('says so when there was nothing to price', () => {
    const report = revaluationReport([])
    expect(report.title).toBe('Nothing was re-priced')
    expect(report.tone).toBe('neutral')
  })

  it('counts a failed lookup as skipped with its reason', () => {
    const failed = result({ name: 'House 2', skipped: 'camoufox: page timed out' })
    expect(revaluationReport([failed]).description).toBe(
      '1 skipped.\nHouse 2: Camoufox: page timed out.',
    )
  })
})

describe('the one-account report', () => {
  it('gives the new value and the change', () => {
    const report = singleRevaluationReport(repriced, 'USD')
    expect(report.title).toBe('Car 1 re-priced')
    expect(report.description).toBe('Now $17,500.00, -$2,500.00 on its balance.')
    expect(report.tone).toBe('success')
  })

  it('says an estimate that matches the balance changed nothing', () => {
    const report = singleRevaluationReport(unchanged, 'USD')
    expect(report.title).toBe('House 1 is unchanged')
    expect(report.description).toBe('The estimate of $300,000.00 matches its balance.')
  })

  it('says what it priced the car as', () => {
    const report = singleRevaluationReport({ ...repriced, priced_as: roadster }, 'USD')
    expect(report.description).toBe(
      'Now $17,500.00, -$2,500.00 on its balance. Priced as a 2019 Examplemotors Roadster Sport at 41,000 miles. Range $16,100 to $18,900.',
    )
  })

  it('says which house it matched, even when the balance was already right', () => {
    const report = singleRevaluationReport({ ...unchanged, priced_as: cottage }, 'USD')
    expect(report.description).toBe(
      'The estimate of $300,000.00 matches its balance. Matched to 42 Example Way, Sampleton, ZZ 00000.',
    )
  })

  it('gives the reason a lookup was skipped', () => {
    const report = singleRevaluationReport(noEstimate, 'USD')
    expect(report.title).toBe('Car 2 was not re-priced')
    expect(report.description).toBe('The source had no estimate for this asset.')
  })
})

describe('while and after a re-price runs', () => {
  it('names what is being re-priced', () => {
    expect(repricingTitle('Car 1')).toBe('Re-pricing Car 1…')
    expect(repricingTitle()).toBe('Re-pricing every asset…')
  })

  it('says what a failed request did not re-price', () => {
    expect(revaluationFailureTitle('Car 1')).toBe('Car 1 was not re-priced')
    expect(revaluationFailureTitle()).toBe('The assets were not re-priced')
  })

  it('says something even when the server sent no result back', () => {
    expect(singleRevaluationResults([], 'Car 1', 'USD')).toEqual({
      title: 'Car 1 was not re-priced',
      description: 'This server does not price this kind of asset.',
      tone: 'neutral',
    })
  })
})

describe('which accounts offer "Re-price value"', () => {
  const car = {
    kind: 'asset' as const,
    type: 'vehicle',
    connection_id: null,
    simplefin_account_id: null,
    property_address: null,
    vehicle_vin: 'TESTVIN0000000001',
  }
  const house = {
    ...car,
    type: 'real_estate',
    vehicle_vin: null,
    property_address: '42 Example Way, Sampleton, ZZ 00000',
  }
  const both = ['real_estate', 'vehicle']

  it('offers it on a car with a VIN and a house with an address', () => {
    expect(canReprice(car, both)).toBe(true)
    expect(canReprice(house, both)).toBe(true)
  })

  it('needs what the source looks the asset up by', () => {
    expect(canReprice({ ...car, vehicle_vin: null }, both)).toBe(false)
    expect(canReprice({ ...house, property_address: '  ' }, both)).toBe(false)
  })

  it('needs the server to price the type', () => {
    expect(canReprice(car, ['real_estate'])).toBe(false)
    expect(canReprice(house, [])).toBe(false)
  })

  it('leaves a connected account to its bank', () => {
    expect(canReprice({ ...car, connection_id: 'c1' }, both)).toBe(false)
    expect(canReprice({ ...car, simplefin_account_id: 'sfin-1' }, both)).toBe(false)
  })

  it('offers it on assets only', () => {
    expect(canReprice({ ...car, kind: 'cash' }, both)).toBe(false)
    expect(canReprice({ ...car, type: 'other_asset' }, [...both, 'other_asset'])).toBe(false)
  })
})
