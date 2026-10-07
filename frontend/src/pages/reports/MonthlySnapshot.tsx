import { QueryBoundary } from '@/components/QueryBoundary'
import { Card } from '@/components/ui'
import { useMonthlySummary } from '@/lib/clients/reports'

import { MonthlySummaryReport } from './MonthlySummaryReport'

/** Monthly Summary reads `/reports/monthly-summary`, not the engine: that is where bills and subscriptions are dropped from the ranked lists. */
export function MonthlySnapshot({ month }: { month: string }) {
  const summary = useMonthlySummary(month)
  return (
    <Card>
      <QueryBoundary query={summary} rows={8}>
        {(data) => <MonthlySummaryReport summary={data} />}
      </QueryBoundary>
    </Card>
  )
}
