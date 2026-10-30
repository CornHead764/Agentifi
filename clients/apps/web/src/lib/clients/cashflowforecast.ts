/**
 * The estimated cash-flow forecast, shown beside the `/cash-flow` projection
 * and never in place of it. An estimate is shown with its provenance
 * (`describeForecastSource`) or not at all.
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, type MoneyShape } from '@/lib/api'
import { absMoney, type Money } from '@/lib/money'

import { balanceHistoryKeys } from './balancehistory'
import { idsKey, queryString, type IsoDate } from './entities'
import { cashFlowKeys } from './upcoming/keys'

export const cashFlowForecastKeys = {
  forecast: (accountIds: readonly string[] | null, horizons: string) =>
    ['cash-flow-forecast', idsKey(accountIds), horizons] as const,
}

export interface ForecastMonth {
  month: string
  money_in: Money
  money_out: Money
  net: Money
}

export interface ForecastWindow {
  days: number
  through: IsoDate
  money_in: Money
  money_out: Money
  net: Money
  /** How many of the window's days the estimate reaches. */
  covered_days: number
  is_complete: boolean
  /** Null together when the projection could not be read. */
  estimated_balance: Money | null
  scheduled_balance: Money | null
  difference: Money | null
}

export interface CashFlowForecast {
  available: boolean
  /** Why there is no estimate, as a sentence. */
  unavailable?: string
  /**
   * `model` is the household forecast an automation writes; `average` is one
   * account's own, re-averaged nightly.
   */
  method?: 'model' | 'average'
  account_id?: string
  model?: string
  generated_at?: string
  age_days?: number
  narrative?: string
  from?: IsoDate
  through?: IsoDate
  months?: ForecastMonth[]
  windows?: ForecastWindow[]
}

export const CASH_FLOW_FORECAST_SHAPE: MoneyShape<CashFlowForecast> = {
  months: { money_in: 'money', money_out: 'money', net: 'money' },
  windows: {
    money_in: 'money',
    money_out: 'money',
    net: 'money',
    estimated_balance: 'money',
    scheduled_balance: 'money',
    difference: 'money',
  },
}

/**
 * `accountIds` keeps the comparison to the accounts the chart shows; null is
 * every account. Exactly one account gets its own averaged estimate.
 */
export function useCashFlowForecast(
  accountIds: readonly string[] | null,
  horizons: readonly number[],
) {
  const spelled = horizons.join(',')
  return useQuery({
    queryKey: cashFlowForecastKeys.forecast(accountIds ?? null, spelled),
    queryFn: ({ signal }) =>
      api.get<CashFlowForecast>(
        `/cash-flow-forecast${queryString({ horizons: spelled }, { account_id: accountIds })}`,
        CASH_FLOW_FORECAST_SHAPE,
        signal,
      ),
  })
}

/** The projection and history are derived on read, so refetching them re-runs them. */
export function useRerunCashFlowForecast(
  accountId: string,
  horizons: readonly number[],
) {
  const client = useQueryClient()
  const spelled = horizons.join(',')
  const ids = [accountId]
  return useMutation({
    meta: { failure: false },
    mutationFn: () =>
      api.post<CashFlowForecast>(
        `/cash-flow-forecast/run${queryString({ horizons: spelled }, { account_id: ids })}`,
        undefined,
        CASH_FLOW_FORECAST_SHAPE,
      ),
    onSuccess: async (forecast) => {
      client.setQueryData(cashFlowForecastKeys.forecast(ids, spelled), forecast)
      await Promise.all([
        client.invalidateQueries({ queryKey: cashFlowKeys.all }),
        client.invalidateQueries({ queryKey: balanceHistoryKeys.all }),
      ])
    },
  })
}

export function forecastWindow(
  forecast: CashFlowForecast | undefined,
  days: number,
): ForecastWindow | null {
  if (!forecast?.available) return null
  return forecast.windows?.find((one) => one.days === days) ?? null
}

/** The one place this sentence is written; it keeps an estimate from reading as arithmetic. */
export function describeForecastSource(forecast: CashFlowForecast): string {
  const model = forecast.model?.trim()
  const age = forecast.age_days ?? 0
  const when = age <= 0 ? 'today' : age === 1 ? 'yesterday' : `${age} days ago`
  if (forecast.method === 'average') return `Averaged from this account’s history, ${when}`
  return model ? `Estimated by ${model}, ${when}` : `Estimated ${when}`
}

/** Null when the projection could not be read, rather than a zero. */
export function describeForecastGap(
  window: ForecastWindow,
  format: (value: Money) => string,
): string | null {
  if (window.difference === null || window.scheduled_balance === null) return null
  const scheduled = format(window.scheduled_balance)
  if (window.difference === 0) return `the same as the scheduled ${scheduled}`
  const gap = format(absMoney(window.difference))
  return window.difference < 0
    ? `${gap} below the scheduled ${scheduled}`
    : `${gap} above the scheduled ${scheduled}`
}
