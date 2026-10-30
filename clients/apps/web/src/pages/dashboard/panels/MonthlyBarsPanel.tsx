import { BarSeries } from '@/components/charts'
import { EXPENSE_COLOR, INCOME_COLOR } from '@/components/charts'
import { QueryBoundary } from '@/components/QueryBoundary'
import { useMonthlyTotals } from '@/lib/clients/dashboard'
import { axisLabels } from '@/lib/format'
import { ZERO_MONEY, type Money as MoneyValue } from '@/lib/money'

export function MonthlyBarsPanel({ direction }: { direction: 'income' | 'spending' }) {
  const report = useMonthlyTotals(direction, 6)
  return (
    <QueryBoundary query={report} rows={4}>
      {(data) => (
        <BarSeries
          height={180}
          color={direction === 'income' ? INCOME_COLOR : EXPENSE_COLOR}
          signs={direction === 'income' ? 'stored' : 'absolute'}
          points={monthColumns(data.summary?.columns ?? [], data.summary?.column_totals ?? [])}
        />
      )}
    </QueryBoundary>
  )
}

/** Month-grain summary columns, labeled so a multi-year window stays unique. */
function monthColumns(
  columns: readonly { key: string }[],
  totals: readonly MoneyValue[],
) {
  const labels = axisLabels(columns.map((column) => `${column.key}-01`))
  return columns.map((column, index) => ({
    key: column.key,
    label: labels[index],
    value: totals[index] ?? ZERO_MONEY,
  }))
}
