import { describe, expect, it } from 'vitest'

import { coerceMoney } from '@/lib/api'
import { formatMoney, type Money } from '@/lib/money'

import {
  CASH_FLOW_FORECAST_SHAPE,
  describeForecastGap,
  describeForecastSource,
  forecastWindow,
  type CashFlowForecast,
} from './cashflowforecast'

const wire = {
  available: true,
  model: 'example-model',
  generated_at: '2026-09-13T05:45:12Z',
  age_days: 2,
  narrative: 'Two paychecks a month against about 2,400 of everyday spending.',
  from: '2026-09-13',
  through: '2027-02-28',
  months: [{ month: '2026-09', money_in: '8450.00', money_out: '7310.00', net: '1140.00' }],
  windows: [
    {
      days: 30,
      through: '2026-10-13',
      money_in: '8450.00',
      money_out: '9100.50',
      net: '-650.50',
      covered_days: 31,
      is_complete: true,
      estimated_balance: '3349.50',
      scheduled_balance: '4200.00',
      difference: '-850.50',
    },
    {
      days: 180,
      through: '2027-03-12',
      money_in: '50700.00',
      money_out: '48000.00',
      net: '2700.00',
      covered_days: 169,
      is_complete: false,
      estimated_balance: null,
      scheduled_balance: null,
      difference: null,
    },
  ],
}

describe('the forecast crosses the wire as strings', () => {
  it('coerces every amount and leaves a missing comparison null', () => {
    const forecast = coerceMoney<CashFlowForecast>(
      structuredClone(wire),
      CASH_FLOW_FORECAST_SHAPE,
    )
    expect(formatMoney(forecast.months![0].money_in)).toBe('$8,450.00')
    expect(formatMoney(forecast.windows![0].difference!)).toBe('-$850.50')
    expect(forecast.windows![1].estimated_balance).toBeNull()
  })
})

describe('forecastWindow', () => {
  const forecast = coerceMoney<CashFlowForecast>(structuredClone(wire), CASH_FLOW_FORECAST_SHAPE)

  it('finds the window for the range the card is showing', () => {
    expect(forecastWindow(forecast, 30)?.through).toBe('2026-10-13')
    expect(forecastWindow(forecast, 180)?.is_complete).toBe(false)
  })

  it('has nothing for a range the estimate does not cover', () => {
    expect(forecastWindow(forecast, 90)).toBeNull()
  })

  it('has nothing at all when no forecast is available', () => {
    expect(forecastWindow(undefined, 30)).toBeNull()
    expect(forecastWindow({ available: false, unavailable: 'No forecast yet.' }, 30)).toBeNull()
  })
})

describe('describeForecastGap', () => {
  const forecast = coerceMoney<CashFlowForecast>(structuredClone(wire), CASH_FLOW_FORECAST_SHAPE)

  it('says how far the estimate sits below the arithmetic', () => {
    expect(describeForecastGap(forecast.windows![0], formatMoney)).toBe(
      '$850.50 below the scheduled $4,200.00',
    )
  })

  it('says nothing when there was no projection to compare against', () => {
    expect(describeForecastGap(forecast.windows![1], formatMoney)).toBeNull()
  })

  it('reads the other way round when the estimate is the rosier one', () => {
    const rosier = { ...forecast.windows![0], difference: 20_000 as Money }
    expect(describeForecastGap(rosier, formatMoney)).toBe('$200.00 above the scheduled $4,200.00')
  })
})

describe('describeForecastSource', () => {
  it('names the model and how long ago it answered', () => {
    expect(describeForecastSource({ available: true, model: 'example-model', age_days: 2 })).toBe(
      'Estimated by example-model, 2 days ago',
    )
    expect(describeForecastSource({ available: true, model: 'example-model', age_days: 1 })).toBe(
      'Estimated by example-model, yesterday',
    )
    expect(describeForecastSource({ available: true, model: 'example-model', age_days: 0 })).toBe(
      'Estimated by example-model, today',
    )
  })

  it('still says it is an estimate when no model was recorded', () => {
    expect(describeForecastSource({ available: true })).toBe('Estimated today')
  })

  it('names an account’s own average as an average, not a model', () => {
    expect(describeForecastSource({ available: true, method: 'average', age_days: 0 })).toBe(
      'Averaged from this account’s history, today',
    )
  })
})
