import { ChevronRight } from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { InfoTip } from '@/components/InfoTip'
import { Money } from '@/components/Money'
import { Badge, Button, Card, IconButton, type BadgeTone } from '@/components/ui'
import type { SavingsRating, SpendingReportData } from '@/lib/clients/reports'
import { capitalize, formatDate, formatPercent, parseRate } from '@/lib/format'
import type { Money as MoneyValue } from '@/lib/money'
import { registerLinkForWindow } from '@/lib/transactions/links'

const RATING_TONES: Record<SavingsRating, BadgeTone> = {
  none: 'expense',
  low: 'warning',
  good: 'income',
  great: 'income',
}

/** Spending as the card prints it: the stored sign, or a "+" for a net credit. */
function SpentAmount({ value, showCents }: { value: MoneyValue; showCents: boolean }) {
  if (value > 0) return <Money value={value} showPlus tone="signed" showCents={showCents} />
  return <Money value={value} tone="neutral" showCents={showCents} />
}

/** The muted line beneath a card's figure: what the period is expected to close at. */
function Expected({ by, children }: { by: string; children: ReactNode }) {
  return (
    <p className="spend-cards__expected">
      Expected by {by}: {children}
    </p>
  )
}

/**
 * Income, spending, what was left or overspent, and the savings rate, for the
 * selected period. The period in progress adds what it is expected to close
 * at, counting the bills and income still scheduled, and its rating is that
 * projection's.
 */
export function SummaryCards({
  data,
  showCents,
  spendingRate,
  onSpendingRate,
}: {
  data: SpendingReportData
  showCents: boolean
  /** Show spending as a share of income instead of the savings rate. */
  spendingRate: boolean
  onSpendingRate: (on: boolean) => void
}) {
  const { summary } = data
  const window = { from: data.window.from, to: data.window.to }
  const rate = parseRate(spendingRate ? summary.spending_rate : summary.savings_rate)
  const over = summary.remaining < 0
  const projection = summary.projection !== null && summary.projection.count > 0 ? summary.projection : null
  const by = projection ? formatDate(projection.end, 'short') : ''
  const expectedRate = projection
    ? parseRate(spendingRate ? projection.spending_rate : projection.savings_rate)
    : null
  const rated = projection ? expectedRate : rate
  const badge =
    spendingRate || rated === null ? null : (
      <Badge tone={RATING_TONES[summary.rating]}>{capitalize(summary.rating)}</Badge>
    )

  return (
    <div className="spend-cards">
      <Card
        title="Income"
        actions={
          <IconButton label="Show this income in the register" variant="ghost" size="sm" asChild>
            <Link to={registerLinkForWindow(window, { tab: 'income' })}>
              <ChevronRight size={14} />
            </Link>
          </IconButton>
        }
      >
        <p className="figure--stat">
          <Money value={summary.income} showPlus tone="flow" showCents={showCents} />
        </p>
        {projection ? (
          <Expected by={by}>
            <Money value={projection.income} showPlus tone="neutral" showCents={showCents} />
          </Expected>
        ) : null}
      </Card>
      <Card title={summary.spent > 0 ? 'Net credit' : 'Total spent'}>
        <p className="figure--stat">
          <SpentAmount value={summary.spent} showCents={showCents} />
        </p>
        {projection ? (
          <Expected by={by}>
            <Money value={projection.spent} showPlus={projection.spent > 0} tone="neutral" showCents={showCents} />
          </Expected>
        ) : null}
      </Card>
      <Card title={over ? 'Overspent' : 'Remaining'}>
        <p className="figure--stat">
          <Money value={summary.remaining} tone="signed" showCents={showCents} />
        </p>
        {projection ? (
          <Expected by={by}>
            <Money value={projection.remaining} tone="neutral" showCents={showCents} />
          </Expected>
        ) : null}
      </Card>
      <Card
        title={
          <span className="spend-cards__title">
            {spendingRate ? 'Spending rate' : 'Savings rate'}
            <InfoTip label={spendingRate ? 'About the spending rate' : 'About the savings rate'}>
              <p className="spend-cards__tip-title">{spendingRate ? 'Spending rate' : 'Savings rate'}</p>
              <p>
                {spendingRate
                  ? 'The percentage of your income you spent. Lower means you’re saving more.'
                  : 'The percentage of your income left after spending. Higher means you’re saving more.'}
              </p>
              {projection ? (
                <p>
                  This period runs to {by}. The expected figures add the bills and income still scheduled
                  before then{spendingRate ? '' : ', and the rating is for the expected savings rate'}.
                </p>
              ) : null}
              <p className="muted">Tip: aim for a 15–30% savings rate.</p>
              <Button size="sm" onClick={() => onSpendingRate(!spendingRate)}>
                {spendingRate ? 'Switch to savings rate' : 'Switch to spending rate'}
              </Button>
            </InfoTip>
          </span>
        }
      >
        <p className="figure--stat spend-cards__rate">
          <span className="money">{formatPercent(projection ? expectedRate : rate, { digits: 1 })}</span>
          {badge}
        </p>
        {projection ? (
          <p className="spend-cards__expected">
            Expected by {by}; so far <span className="money">{formatPercent(rate, { digits: 1 })}</span>
          </p>
        ) : null}
      </Card>
    </div>
  )
}
