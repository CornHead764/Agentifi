import { AreaTrend, labeledPoints } from '@/components/charts'
import { ChangeBadge } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Card, EmptyState, Table, Td, Th } from '@/components/ui'
import { useSavingsReport } from '@/lib/clients/reports'
import { formatMonthKey } from '@/lib/format'

/** Savings reads its own endpoint: balances in savings accounts, not transactions, with a per-account month pivot. */
export function SavingsReport({ from, to }: { from: string | null; to: string }) {
  const report = useSavingsReport(from, to)
  return (
    <QueryBoundary query={report} rows={6}>
      {(data) => (
        <>
          <Card title="Balance in savings">
            <div className="headline">
              <Money value={data.end} tone="neutral" className="figure--total" />
              <ChangeBadge amount={data.change} rate={data.change_pct} caption="over this window" />
            </div>
            <AreaTrend
              points={labeledPoints(data.points, (point) => point.balance)}
            />
          </Card>

          <Card title="Savings by month" flush>
            {data.accounts.length === 0 ? (
              <div className="card__body">
                <EmptyState
                  title="No savings accounts"
                  body="This report reads accounts whose type is savings."
                />
              </div>
            ) : (
              <Table density="sm" className="pivot">
                <thead>
                  <tr>
                    <Th className="pivot__head">Account</Th>
                    {data.months.map((month) => (
                      <Th key={month} numeric>
                        {formatMonthKey(month)}
                      </Th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {data.accounts.map((row) => (
                    <tr key={row.account_id} className="report__leaf">
                      <Td className="pivot__head">{row.name}</Td>
                      {row.cells.map((cell, index) => (
                        <Td key={data.months[index]} numeric>
                          <Money value={cell} tone="neutral" />
                        </Td>
                      ))}
                    </tr>
                  ))}
                  <tr className="report__grand">
                    <Td className="pivot__head">Total savings</Td>
                    {data.totals.map((total, index) => (
                      <Td key={data.months[index]} numeric>
                        <Money value={total} tone="neutral" />
                      </Td>
                    ))}
                  </tr>
                </tbody>
              </Table>
            )}
          </Card>
        </>
      )}
    </QueryBoundary>
  )
}
