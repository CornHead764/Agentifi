/**
 * Monthly Summary, all from `/reports/monthly-summary`, with Top Categories
 * and Top Payees arriving with bills and subscriptions already dropped.
 * Nothing here ranks, sums or filters: re-ranking from a wider set would put
 * the bills back. `vs <prior month>` is an em dash when there is nothing to
 * compare against.
 */

import { BarSeries } from '@/components/charts'
import { EXPENSE_COLOR, INCOME_COLOR } from '@/components/charts'
import { Money } from '@/components/Money'
import { PercentText, Stat } from '@/components/figures'
import { Badge, Card, EmptyState, List, ListRow } from '@/components/ui'
import type { MonthlySummary, MonthlySummaryEntry } from '@/lib/clients/reports'
import { formatDate, formatPercentUnits, parseRate } from '@/lib/format'
import type { Money as MoneyValue } from '@/lib/money'

export function MonthlySummaryReport({ summary }: { summary: MonthlySummary }) {
  const priorName = formatDate(`${summary.prior_month}-01`, 'monthLong').split(' ')[0]

  return (
    <div className="snapshot">
      <header className="snapshot__head">
        <p className="snapshot__hello">Hello!</p>
        <h2 className="snapshot__title">
          Here’s your {formatDate(`${summary.month}-01`, 'monthLong')} summary.
        </h2>
      </header>

      <Card>
        <div className="snapshot__totals">
          <div className="snapshot__figures">
            <Figure
              label="Total income"
              value={summary.income}
              change={summary.income_change_pct}
              priorName={priorName}
            />
            <Figure
              label="Total expenses"
              value={summary.expenses}
              change={summary.expenses_change_pct}
              priorName={priorName}
            />
          </div>

          <div className="snapshot__charts">
            <BarSeries
              height={120}
              color={INCOME_COLOR}
              points={[{ key: 'current', label: 'This month', value: summary.income }]}
            />
            <BarSeries
              height={120}
              color={EXPENSE_COLOR}
              signs="absolute"
              points={[
                { key: 'bills', label: 'Bills', value: summary.bills },
                { key: 'other', label: 'Everything else', value: summary.discretionary },
              ]}
            />
            <p className="snapshot__annotation">
              <Money value={summary.bills} signs="absolute" tone="neutral" /> on bills ·{' '}
              <Money value={summary.discretionary} signs="absolute" tone="neutral" /> on all other
              expenses
            </p>
          </div>

          <div className="snapshot__net">
            <p className="stat__label">Net income</p>
            <p className="figure--total">
              <Money value={summary.net} />
            </p>
            <p className="muted">{comparison(summary.net_change_pct, priorName)}</p>
          </div>
        </div>
      </Card>

      <h3 className="snapshot__section">Your spending excluding bills and subscriptions</h3>

      <div className="snapshot__lists">
        <RankedList
          title="Top categories"
          priorName={priorName}
          entries={summary.top_categories}
          showCounts
        />
        <RankedList
          title="Top payees"
          priorName={priorName}
          entries={summary.top_payees}
          showCounts
        />
      </div>
    </div>
  )
}

/** "96% higher than June", or the absence of a comparison — never "0% higher". */
function comparison(rate: string | number | null, priorName: string): string {
  const percent = parseRate(rate)
  if (percent === null) return `No comparison against ${priorName}`
  return `${formatPercentUnits(Math.abs(percent), { digits: 0 })} ${
    percent >= 0 ? 'higher' : 'lower'
  } than ${priorName}`
}

function Figure({
  label,
  value,
  change,
  priorName,
}: {
  label: string
  value: MoneyValue
  change: string | number | null
  priorName: string
}) {
  return (
    <Stat label={label} sub={comparison(change, priorName)}>
      <Money value={value} signs="absolute" tone="neutral" />
    </Stat>
  )
}

function RankedList({
  title,
  entries,
  priorName,
  showCounts = false,
}: {
  title: string
  entries: readonly MonthlySummaryEntry[]
  priorName: string
  showCounts?: boolean
}) {
  return (
    <Card title={title} subtitle={`vs ${priorName}`}>
      {entries.length === 0 ? (
        <EmptyState
          compact
          title="Nothing to rank: no spending this month outside bills and subscriptions."
        />
      ) : (
        <List>
          {entries.map((entry) => (
            <ListRow
              key={entry.key}
              title={<span title={entry.label}>{entry.label}</span>}
              badge={showCounts ? <Badge>{entry.count}x</Badge> : undefined}
              figures={<Money value={entry.total} signs="absolute" tone="neutral" />}
              figuresSub={<PercentText rate={entry.change_pct} digits={0} showPlus tone="neutral" />}
            />
          ))}
        </List>
      )}
    </Card>
  )
}
