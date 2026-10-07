import { Download, Printer } from 'lucide-react'
import { useMemo, type ReactNode } from 'react'

import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Card, EmptyState, IconButton } from '@/components/ui'
import {
  useReportRun,
  type ReportConfig,
  type ReportQuery,
  type ReportResult,
} from '@/lib/clients/reports'
import { accountNamer } from '@/lib/accounts'
import { categoryName } from '@/lib/categoryNames'
import { formatDate } from '@/lib/format'
import type { Money as MoneyValue } from '@/lib/money'
import { reportToCsv } from '@/lib/reports/csv'
import { DIMENSION_LABELS } from '@/lib/reports/labels'
import { useAccounts, useCategories } from '@/lib/transactions/queries'

import { DonutRow, PivotChart } from './EngineCharts'
import { SummaryReport, TransactionReport, type NameLookup } from './renderings'
import { downloadCsv } from '@/lib/csv'

export function EngineReport({
  name,
  query,
  enabled,
  filterFailed,
  toolbar,
}: {
  name: string
  query: ReportQuery
  enabled: boolean
  /** The ad-hoc filter row was not written, so the report is not run. */
  filterFailed: boolean
  /** The report's search, filter and knobs, on the first card's first row. */
  toolbar: ReactNode
}) {
  const result = useReportRun(query, enabled && !filterFailed)
  const accounts = useAccounts()
  const categories = useCategories()

  const names: NameLookup = useMemo(
    () => ({
      account: accountNamer(accounts.data),
      category: (id) => categoryName(categories.data ?? [], id),
    }),
    [accounts.data, categories.data],
  )

  const data = filterFailed ? undefined : result.data

  // The first card stays mounted while the report runs, so the controls that
  // started the run do not leave the screen under the pointer.
  return (
    <>
      <Card
        title={headline(query.config.sign)}
        subtitle={windowCaption(data?.window ?? query)}
        actions={
          <>
            <IconButton
              label="Download CSV"
              size="sm"
              disabled={!data}
              onClick={() => (data ? downloadCsv(`${name}.csv`, reportToCsv(data)) : undefined)}
            >
              <Download size={14} />
            </IconButton>
            <IconButton label="Print" size="sm" disabled={!data} onClick={() => window.print()}>
              <Printer size={14} />
            </IconButton>
          </>
        }
      >
        {toolbar}
        {filterFailed ? (
          <EmptyState
            compact
            title="Could not apply this filter"
            body="The report was not run, rather than run over everything."
          />
        ) : (
          <QueryBoundary query={result} rows={6}>
            {(loaded) => (
              <>
                <div className="headline">
                  <Money value={total(loaded)} tone="neutral" className="figure--total" />
                </div>
                {loaded.summary ? (
                  <PivotChart summary={loaded.summary} grain={loaded.config.time_grain} />
                ) : (
                  <DonutRow result={loaded} />
                )}
              </>
            )}
          </QueryBoundary>
        )}
      </Card>

      {data ? (
        <Card title={data.summary ? 'Summary by group' : 'Transactions by group'} flush>
          {data.summary ? (
            <SummaryReport
              result={data.summary}
              heading={DIMENSION_LABELS[data.config.rows]}
              grain={data.config.time_grain}
            />
          ) : data.transaction ? (
            <TransactionReport
              result={data.transaction}
              heading={DIMENSION_LABELS[data.config.rows]}
              names={names}
            />
          ) : null}
        </Card>
      ) : null}
    </>
  )
}

function total(result: ReportResult): MoneyValue {
  return result.summary?.total ?? result.transaction?.total ?? result.totals.net
}

function headline(sign: ReportConfig['sign']): string {
  switch (sign) {
    case 'income':
      return 'Total income'
    case 'expenses':
      return 'Total expenses'
    default:
      return 'Total net income'
  }
}

function windowCaption(window: { from: string | null; to: string | null }): string {
  const to = window.to ? formatDate(window.to) : 'today'
  return window.from ? `${formatDate(window.from)} – ${to}` : `Through ${to}`
}
