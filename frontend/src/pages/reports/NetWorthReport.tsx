import { AreaTrend, labeledPoints } from '@/components/charts'
import { ChangeBadge } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Card } from '@/components/ui'
import { useNetWorthWindow } from '@/lib/clients/networth'

export function NetWorthReport({ from, to }: { from: string | null; to: string }) {
  const series = useNetWorthWindow(from, to)
  return (
    <Card title="Net worth over time">
      <QueryBoundary query={series} rows={6}>
        {(data) => (
          <>
            <div className="headline">
              <Money value={data.end.net} tone="neutral" className="figure--total" />
              <ChangeBadge amount={data.change} rate={data.change_pct} caption="over this window" />
            </div>
            <AreaTrend
              points={labeledPoints(data.points, (point) => point.net)}
            />
          </>
        )}
      </QueryBoundary>
    </Card>
  )
}
