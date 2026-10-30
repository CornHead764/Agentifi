/**
 * Where this account's balance is heading, where it has been, or both; the
 * title is the selector. *Projected* is the Bills & Income projection for one
 * account, *Historical* is `/account-balance-history`, and *Both* joins them
 * at today.
 *
 * The model's estimate sits under the chart, labelled, never drawn on it: the
 * chart is arithmetic.
 */

import { ChevronDown, RefreshCw } from 'lucide-react'
import { useContext, useMemo, useState, type ReactNode } from 'react'

import { MultiLine, labeledAxis } from '@/components/charts'
import { ReminderRail } from '@/components/transactions/RemindersStrip'
import { seriesColor } from '@/components/charts/palette'
import { Facts } from '@/components/Facts'
import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Callout,
  CollapsibleCard,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  EmptyState,
  OverflowMenu,
  useProgressToast,
} from '@/components/ui'
import { AuthContext } from '@/contexts/auth'
import { useAccountBalanceHistory } from '@/lib/clients/balancehistory'
import {
  describeForecastGap,
  describeForecastSource,
  forecastWindow,
  useCashFlowForecast,
  useRerunCashFlowForecast,
  type CashFlowForecast,
} from '@/lib/clients/cashflowforecast'
import { occurrenceMarkers, useCashFlow } from '@/lib/clients/upcoming'
import { dayWindow } from '@/lib/dateRanges'
import { formatDate, toIsoDate } from '@/lib/format'
import { moneyToNumber } from '@/lib/money'
import {
  CASH_FLOW_VIEW_LABELS,
  CASH_FLOW_VIEWS,
  HISTORY_RANGES,
  joinCashFlow,
  readCashFlowPrefs,
  writeCashFlowPrefs,
  type CashFlowPrefs,
  type CashFlowView,
  type HistoryRange,
} from '@/lib/transactions/cashFlowView'
import type { Account } from '@/lib/transactions/types'
import { HORIZON_DAYS, horizonOf } from '@/lib/upcoming/horizon'

/**
 * The view and history range, per signed-in user, held with the id they were
 * read for so a session arriving after first paint re-reads.
 */
function useCashFlowPrefs(): [CashFlowPrefs, (next: CashFlowPrefs) => void] {
  // Read without demanding a provider: the register renders under test with
  // none, and a missing session only means the anonymous preference.
  const userId = useContext(AuthContext)?.user?.id ?? null
  const [stored, setStored] = useState(() => ({ userId, prefs: readCashFlowPrefs(userId) }))
  const prefs = stored.userId === userId ? stored.prefs : readCashFlowPrefs(userId)
  const update = (next: CashFlowPrefs) => {
    setStored({ userId, prefs: next })
    writeCashFlowPrefs(userId, next)
  }
  return [prefs, update]
}

export function ProjectedCashFlow({ account }: { account: Account }) {
  const [prefs, setPrefs] = useCashFlowPrefs()
  const [days, setDays] = useState(90)
  const bounds = useMemo(() => horizonOf(days), [days])
  const past = useMemo(() => dayWindow(prefs.historyDays, 0), [prefs.historyDays])
  const showsHistory = prefs.view !== 'projected'
  const showsProjection = prefs.view !== 'historical'
  // One id, never an empty list: an empty account_id is the server's "no
  // accounts", and omitting it is every account.
  const ids = useMemo(() => [account.id], [account.id])
  const flow = useCashFlow(bounds.from, bounds.to, ids)
  const history = useAccountBalanceHistory(account.id, past.from, past.to, showsHistory)
  const forecast = useCashFlowForecast(ids, HORIZON_DAYS)
  const progress = useProgressToast()
  const rerun = useRerunCashFlowForecast(account.id, HORIZON_DAYS)
  const rerunProjection = () =>
    void progress(
      `Re-running ${account.name}'s projection…`,
      rerun.mutateAsync(),
      { title: `Re-ran ${account.name}'s projection`, tone: 'success' },
      'Could not re-run the projection',
    )

  const line = flow.data?.accounts[0] ?? null
  const setView = (view: CashFlowView) => setPrefs({ ...prefs, view })
  const setHistoryDays = (historyDays: HistoryRange) => setPrefs({ ...prefs, historyDays })

  let subtitle: ReactNode = `${formatDate(past.from)} – ${formatDate(past.to)}`
  if (showsProjection && line?.first_below) {
    subtitle = (
      <>
        Dips below zero on {formatDate(line.first_below.on)}, to{' '}
        <Money value={line.first_below.balance} />
      </>
    )
  } else if (prefs.view === 'projected') {
    subtitle = `${formatDate(bounds.from)} – ${formatDate(bounds.to)}`
  } else if (prefs.view === 'both') {
    subtitle = `${formatDate(past.from)} – ${formatDate(bounds.to)}`
  }

  const rangeLabel =
    prefs.view === 'projected'
      ? `Next ${days} days`
      : prefs.view === 'historical'
        ? `Last ${prefs.historyDays} days`
        : `${prefs.historyDays} back · ${days} ahead`

  return (
    <CollapsibleCard
      className="cash-flow-card"
      aria-busy={rerun.isPending}
      storageKey="cashflow.collapsed"
      what="the chart"
      title={
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button type="button" className="cash-flow-card__view" aria-label="Choose the chart">
              {CASH_FLOW_VIEW_LABELS[prefs.view]} <ChevronDown size={14} aria-hidden />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuRadioGroup
              value={prefs.view}
              onValueChange={(value) => {
                const view = CASH_FLOW_VIEWS.find((one) => one === value)
                if (view) setView(view)
              }}
            >
              {CASH_FLOW_VIEWS.map((view) => (
                <DropdownMenuRadioItem key={view} value={view}>
                  {view === 'both' ? 'Both' : CASH_FLOW_VIEW_LABELS[view]}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      }
      subtitle={subtitle}
      actions={
        <>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="sm">
                {rangeLabel} <ChevronDown size={13} />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              {showsHistory ? (
                <>
                  {showsProjection ? <DropdownMenuLabel>Look back</DropdownMenuLabel> : null}
                  {HISTORY_RANGES.map((option) => (
                    <DropdownMenuItem key={`back-${option}`} onSelect={() => setHistoryDays(option)}>
                      Last {option} days
                    </DropdownMenuItem>
                  ))}
                </>
              ) : null}
              {showsHistory && showsProjection ? <DropdownMenuSeparator /> : null}
              {showsProjection ? (
                <>
                  {showsHistory ? <DropdownMenuLabel>Look ahead</DropdownMenuLabel> : null}
                  {HORIZON_DAYS.map((option) => (
                    <DropdownMenuItem key={`ahead-${option}`} onSelect={() => setDays(option)}>
                      Next {option} days
                    </DropdownMenuItem>
                  ))}
                </>
              ) : null}
            </DropdownMenuContent>
          </DropdownMenu>
          <OverflowMenu
            label="Actions for the cash flow"
            busy={rerun.isPending}
            actions={[
              {
                label: 'Re-run projection',
                icon: <RefreshCw size={14} />,
                disabled: rerun.isPending,
                onSelect: rerunProjection,
              },
            ]}
          />
        </>
      }
    >
      {prefs.view === 'projected' ? (
        <QueryBoundary query={flow} rows={6}>
          {(data) => {
            const only = data.accounts[0]
            if (!only || only.points.length === 0) {
              return (
                <EmptyState
                  compact
                  title={`Nothing scheduled in the next ${days} days. Projection = today’s balance.`}
                />
              )
            }
            const axis = only.points.map((point) => point.on)
            return (
              <MultiLine
                height={220}
                axis={labeledAxis(axis)}
                series={[
                  {
                    key: only.account_id,
                    label: only.name,
                    color: seriesColor(0),
                    dashed: true,
                    markers: occurrenceMarkers(only.account_id, data.occurrences, moneyToNumber),
                    points: Object.fromEntries(
                      only.points.map((point) => [point.on, moneyToNumber(point.balance)]),
                    ),
                  },
                ]}
              />
            )
          }}
        </QueryBoundary>
      ) : prefs.view === 'historical' ? (
        <QueryBoundary query={history} rows={6}>
          {(data) => {
            if (data.points.length === 0) {
              return <EmptyState compact title="This account has no balance to show for these days." />
            }
            const axis = data.points.map((point) => point.on)
            return (
              <MultiLine
                height={220}
                axis={labeledAxis(axis)}
                series={[
                  {
                    key: 'history',
                    label: account.name,
                    color: seriesColor(0),
                    points: Object.fromEntries(
                      data.points.map((point) => [point.on, moneyToNumber(point.balance)]),
                    ),
                  },
                ]}
              />
            )
          }}
        </QueryBoundary>
      ) : (
        <QueryBoundary query={history} rows={6}>
          {(past) => (
            <QueryBoundary query={flow} rows={6}>
              {(data) => {
                const only = data.accounts[0]
                const joined = joinCashFlow(
                  past.points.map((point) => ({
                    on: point.on,
                    balance: moneyToNumber(point.balance),
                  })),
                  (only?.points ?? []).map((point) => ({
                    on: point.on,
                    balance: moneyToNumber(point.balance),
                  })),
                  toIsoDate(new Date()),
                )
                if (joined.axis.length === 0) {
                  return <EmptyState compact title="This account has no balance to show yet." />
                }
                return (
                  <MultiLine
                    height={220}
                    axis={labeledAxis(joined.axis)}
                    markAt={joined.today ? { key: joined.today, label: 'Today' } : undefined}
                    series={[
                      {
                        key: 'history',
                        label: 'Actual',
                        color: seriesColor(0),
                        points: joined.history,
                      },
                      {
                        key: 'projection',
                        label: 'Projected',
                        color: seriesColor(0),
                        dashed: true,
                        markers: only
                          ? occurrenceMarkers(only.account_id, data.occurrences, moneyToNumber)
                          : undefined,
                        points: joined.projection,
                      },
                    ]}
                  />
                )
              }}
            </QueryBoundary>
          )}
        </QueryBoundary>
      )}
      {showsProjection ? <ForecastNote days={days} forecast={forecast.data} /> : null}
      <ReminderRail accountIds={ids} />
    </CollapsibleCard>
  )
}

/**
 * The estimate, under the line: who made it, how long ago, and how far it
 * disagrees with the projection. Partial coverage says so. A failed request
 * renders nothing.
 */
function ForecastNote({
  days,
  forecast,
}: {
  days: number
  forecast: CashFlowForecast | undefined
}) {
  const moneyText = useMoneyText()
  if (!forecast) return null
  if (!forecast.available) {
    return forecast.unavailable ? (
      <Callout className="cash-flow-estimate__none">{forecast.unavailable}</Callout>
    ) : null
  }
  const range = forecastWindow(forecast, days)
  if (!range) return null
  const gap = describeForecastGap(range, moneyText)

  return (
    <section className="cash-flow-estimate" aria-label="Estimated cash flow">
      <p className="row row--wrap cash-flow-estimate__head">
        <Badge tone="neutral">Estimate</Badge>
        <span className="muted">{describeForecastSource(forecast)}</span>
      </p>
      <Facts
        className="cash-flow-estimate__figures"
        facts={[
          {
            label: `Net, next ${days} days`,
            value: <Money value={range.net} showPlus />,
          },
          range.estimated_balance !== null && {
            label: `Balance by ${formatDate(range.through)}`,
            value: (
              <>
                ≈ <Money value={range.estimated_balance} />
              </>
            ),
          },
          range.estimated_balance !== null && gap !== null && { label: 'Vs. schedule', value: gap },
          !range.is_complete && {
            label: 'Covers',
            value: `${range.covered_days} of ${range.days + 1} days, to ${formatDate(forecast.through ?? range.through)}`,
          },
        ]}
      />
      {forecast.narrative ? <p className="muted">{forecast.narrative}</p> : null}
    </section>
  )
}
