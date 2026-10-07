/**
 * One security's own screen: what it has done, where it is, and what it cost.
 *
 * - **An empty price chart stays empty.** `securities` keeps only the latest
 *   quote and the close before it, and one point drawn as a flat line would
 *   read as a quiet week; the card says what it has and offers the fetch.
 * - **The position figures come from the portfolio read**, so a security held
 *   in three accounts totals exactly as its three table rows do.
 * - **Unknown cost basis travels.** A basis missing in any account makes the
 *   whole position incomplete, badged as the header badges it.
 */

import { Download } from 'lucide-react'
import { useState } from 'react'

import { AreaTrend, labeledPoints } from '@/components/charts'
import { MoneyOrDash, PercentText, PriceText, Stat } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { RangeChips } from '@/components/RangeChips'
import {
  Badge,
  Button,
  Dialog,
  DialogContent,
  EmptyState,
  Table,
  TableEmptyRow,
  Td,
  Th,
  useProgressToast,
  WarningMark,
} from '@/components/ui'
import type { RangePreset } from '@/lib/dateRanges'
import {
  useBackfillHistory,
  useInvestmentActivity,
  useSecurityDetail,
  type ActivityRow,
  type SecurityDetail,
} from '@/lib/clients/investments'
import {
  formatDate,
  formatShares,
  fractionToPercentUnits,
  parseRate,
} from '@/lib/format'
import { parseMoney } from '@/lib/money'

import { activityLabel, mentionsSecurity } from './activityKinds'
import { NO_BASIS, NO_CLOSE, positionsMissingBasisText } from './incomplete'
import { AskRowButton } from '@/components/assistant/AskMenuItem'
import { holdingSubject } from '@/lib/assistant/subjects'

const RANGES: readonly RangePreset[] = ['1M', '3M', '6M', '1Y', '5Y']


export function HoldingDetailDialog({
  securityId,
  symbol,
  accountName,
  onClose,
}: {
  /** Null keeps the dialog shut; the id is also the query. */
  securityId: string | null
  symbol: string
  accountName: (id: string) => string
  onClose: () => void
}) {
  const [range, setRange] = useState<RangePreset>('1Y')
  const detail = useSecurityDetail(securityId, range)

  const progress = useProgressToast()
  const backfill = useBackfillHistory()

  const fetchHistory = () => {
    if (securityId === null) return
    void progress(
      `Fetching ${symbol}'s history…`,
      backfill.mutateAsync({ securityId, range }),
      (result) => ({
        title:
          result.stored === 0
            ? `No public history for ${result.symbol}`
            : `Stored ${result.stored} closes for ${result.symbol}`,
        description:
          result.stored === 0 ? 'Employer plan funds have no public quote.' : undefined,
        tone: result.stored === 0 ? 'error' : 'success',
      }),
      'No history was fetched',
    )
  }

  return (
    <Dialog open={securityId !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent title={symbol} description="Everything on file for this security." wide>
        <QueryBoundary query={detail} rows={6}>
          {(data) => (
            <div className="holding-detail">
              <DetailHeader detail={data} />

              <div className="toolbar toolbar--flush">
                <RangeChips value={range} onChange={setRange} presets={RANGES} />
                <span className="toolbar__spacer" />
                <AskRowButton
                  subject={() =>
                    holdingSubject({
                      security_id: data.security.id,
                      symbol: data.security.symbol,
                      name: data.security.name,
                    })
                  }
                />
                <Button size="sm" onClick={fetchHistory} disabled={backfill.isPending}>
                  <Download size={13} /> {backfill.isPending ? 'Fetching…' : 'Fetch'}
                </Button>
              </div>

              <PriceChart detail={data} />

              <h3 className="holding-detail__heading">Positions</h3>
              <Table density="sm" stack>
                <thead>
                  <tr>
                    <Th>Account</Th>
                    <Th numeric>Shares</Th>
                    <Th numeric>Value</Th>
                    <Th numeric>Total cost</Th>
                    <Th numeric>Gain $</Th>
                  </tr>
                </thead>
                <tbody>
                  {data.positions.length === 0 ? (
                    <TableEmptyRow colSpan={5}>
                      Nobody holds this security today. Its price history stands.
                    </TableEmptyRow>
                  ) : (
                    data.positions.map((position) => (
                      <tr key={position.id}>
                        <Td label="" className="stack-lead">
                          {accountName(position.account_id)}
                        </Td>
                        <Td numeric className="stack-inline">
                          {formatShares(position.shares)}
                        </Td>
                        <Td numeric className="stack-inline">
                          <Money value={position.market_value} tone="neutral" />
                        </Td>
                        <Td numeric className="stack-inline">
                          <MoneyOrDash
                            value={position.cost_basis}
                            reason={NO_BASIS}
                            tone="neutral"
                          />
                        </Td>
                        <Td numeric className="stack-inline">
                          <MoneyOrDash value={position.total_gain} reason={NO_BASIS} showPlus />
                        </Td>
                      </tr>
                    ))
                  )}
                </tbody>
              </Table>

              <SecurityActivity detail={data} range={range} accountName={accountName} />
            </div>
          )}
        </QueryBoundary>
      </DialogContent>
    </Dialog>
  )
}

/**
 * The rows that look like they are about this security. Nothing in the schema
 * links a transaction to a security, so this reads the bank's wording, and the
 * heading says "mentioning" rather than implying a join.
 */
function SecurityActivity({
  detail,
  range,
  accountName,
}: {
  detail: SecurityDetail
  range: RangePreset
  accountName: (id: string) => string
}) {
  const accountIds = detail.positions.map((position) => position.account_id)
  const activity = useInvestmentActivity(range, accountIds.length > 0 ? accountIds : null)
  const rows = (activity.data?.items ?? []).filter((row: ActivityRow) =>
    mentionsSecurity(row, detail.security.symbol, detail.security.name),
  )

  return (
    <>
      <h3 className="holding-detail__heading">Activity mentioning {detail.security.symbol}</h3>
      <Table density="sm" stack>
        <thead>
          <tr>
            <Th>Date</Th>
            <Th>Action</Th>
            <Th>Account</Th>
            <Th>Payee</Th>
            <Th numeric>Amount</Th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 ? (
            <TableEmptyRow colSpan={5}>
              No row in this window names it. Matched on the bank’s wording.
            </TableEmptyRow>
          ) : (
            rows.map((row) => (
              <tr key={row.transaction_id}>
                <Td label="" className="stack-lead">
                  {formatDate(row.on)}
                </Td>
                <Td>
                  {activityLabel(row.kind) === null ? null : (
                    <Badge>{activityLabel(row.kind)}</Badge>
                  )}
                </Td>
                <Td>{accountName(row.account_id)}</Td>
                <Td>{row.payee || row.statement_name}</Td>
                <Td numeric className="stack-inline">
                  <Money value={row.amount} />
                </Td>
              </tr>
            ))
          )}
        </tbody>
      </Table>
    </>
  )
}

function DetailHeader({ detail }: { detail: SecurityDetail }) {
  return (
    <div className="stat-row">
      <Stat label="Market value">
        <Money value={detail.market_value} tone="neutral" />
        <span className="stat__aside">{formatShares(detail.shares)} shares</span>
      </Stat>
      <Stat
        label={
          <>
            Total gain
            {detail.is_cost_basis_incomplete ? (
              <WarningMark badge label="Incomplete" side="bottom" text={positionsMissingBasisText(detail)} />
            ) : null}
          </>
        }
      >
        <MoneyOrDash value={detail.total_gain} reason={NO_BASIS} showPlus />
        <PercentText
          rate={fractionToPercentUnits(parseRate(detail.total_gain_pct))}
          showPlus
          className="stat__aside"
        />
      </Stat>
      <Stat label="Today’s change">
        <MoneyOrDash value={detail.day_change} reason={NO_CLOSE} showPlus />
        <PercentText rate={detail.day_change_pct} showPlus className="stat__aside" />
      </Stat>
      <Stat label="Last price">
        {detail.security.last_price === null ? (
          <Badge>No quote</Badge>
        ) : (
          <PriceText price={detail.security.last_price} currency={detail.security.currency} />
        )}
        <span className="stat__aside">{detail.security.name}</span>
      </Stat>
    </div>
  )
}

/** The price line, or the reason there is not one. A close the parser cannot read is dropped rather than plotted as zero. */
function PriceChart({ detail }: { detail: SecurityDetail }) {
  const points = detail.prices
    .filter((point) => point.close !== null)
    .map((point) => ({
      on: point.on,
      // Money on the axis, rounded to the cent for this line only: the stored
      // close keeps all ten places, and every figure derived from it is
      // computed on the server.
      close: parseMoney(String(point.close)),
    }))

  if (points.length < 2) {
    return (
      <EmptyState
        title="No price history on file"
        body="Closes are stored on each refresh. Press Fetch for a year of history."
      />
    )
  }

  return (
    <>
      <div className="stat-row">
        <Stat label={`${formatDate(points[0].on)} to ${formatDate(points.at(-1)!.on)}`}>
          <PercentText rate={fractionToPercentUnits(parseRate(detail.price_change_pct))} showPlus />
        </Stat>
      </div>
      <AreaTrend
        points={labeledPoints(points, (point) => point.close)}
        height={220}
      />
    </>
  )
}
