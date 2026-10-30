import { AreaTrend, labeledPoints } from '@/components/charts'
import { MoneyOrDash, PercentText } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { EmptyState } from '@/components/ui'
import { usePerformance, usePortfolio } from '@/lib/clients/investments'

import { AddInvestmentAction } from './AddInvestmentAction'

export function InvestmentsPanel() {
  const portfolio = usePortfolio(null)
  const series = usePerformance('1M', null)

  return (
    <QueryBoundary query={portfolio} rows={4}>
      {(data) =>
        data.items.length === 0 ? (
          <EmptyState
            title="No portfolio yet"
            body="A brokerage on SimpleFIN arrives with its next sync. Anything else, add by hand and its value is tracked here."
            action={<AddInvestmentAction />}
          />
        ) : (
        <>
          <div className="widget__headline">
            <Money value={data.totals.total_value} tone="neutral" className="figure--total" />
            <PercentText rate={data.totals.day_change_pct} showPlus />
            <MoneyOrDash
              value={data.totals.day_change}
              showPlus
              reason="No prior close on file, so today’s move is unknown."
            />
            <span className="muted">Today</span>
          </div>
          <AreaTrend
            height={140}
            showAxes={false}
            points={labeledPoints(series.data?.points ?? [], (point) => point.value)}
          />
        </>
        )
      }
    </QueryBoundary>
  )
}
