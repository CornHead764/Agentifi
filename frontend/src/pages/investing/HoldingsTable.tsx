/**
 * The Portfolio table and its header, apart from the page so the
 * incomplete-cost-basis rendering is testable without a chart library.
 *
 * An unknown basis arrives as `null`. Total cost shows a warning rather than a
 * figure, the gain beside it a dash, and the totals row is badged
 * **Incomplete** whenever some holdings have a basis and others do not:
 * `market_value − 0` would report the whole position as profit.
 *
 * A missing prior close is the same: day change is unknown, not zero.
 */

import { Trash2 } from 'lucide-react'
import { Fragment, useState } from 'react'

import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { MoneyOrDash, PercentText, PriceText, Stat } from '@/components/figures'
import { Money } from '@/components/Money'
import { withoutSmallBalances } from '@/components/shell/accountTree'
import { SmallBalancesRow } from '@/components/shell/SmallBalancesRow'
import {
  Badge,
  OverflowMenu,
  RowActions,
  SortableTh,
  Table,
  TableEmptyRow,
  TableGroupRow,
  Td,
  Th,
  WarningMark,
} from '@/components/ui'
import { accountNamer } from '@/lib/accounts'
import { holdingSubject } from '@/lib/assistant/subjects'
import type { Holding, PortfolioTotals } from '@/lib/clients/investments'
import { EM_DASH, formatShares, fractionToPercentUnits, parseRate } from '@/lib/format'

import {
  DEFAULT_SORT,
  groupHoldingsByAccount,
  nextSort,
  sortHoldings,
  type HoldingSort,
  type HoldingSortKey,
} from './holdingSort'
import {
  holdingMissingBasisText,
  missingBasisText,
  missingCloseText,
  NO_BASIS,
  NO_CLOSE,
  staleQuoteText,
} from './incomplete'
import type { PriceFreshness } from './priceFreshness'

const NO_QUOTE =
  'No public quote (an employer plan\u2019s own fund, for instance). ' +
  'Value is as the provider reports it.'
export function PortfolioHeader({
  totals,
  holdings,
  asOf,
}: {
  totals: PortfolioTotals
  /** The positions behind the totals, which the Incomplete badges name. */
  holdings: readonly Holding[]
  asOf?: PriceFreshness
}) {
  return (
    <div className="stat-row">
      <Stat label="Market value">
        <Money value={totals.market_value} tone="neutral" />
      </Stat>

      <Stat
        label={
          <>
            Total gain
            {totals.is_cost_basis_incomplete ? (
              <WarningMark badge label="Incomplete" side="bottom" text={missingBasisText(holdings)} />
            ) : null}
          </>
        }
      >
        <MoneyOrDash value={totals.total_gain} reason={NO_BASIS} showPlus />
      </Stat>

      <Stat
        label={
          <>
            Today’s change
            {totals.is_day_change_incomplete ? (
              <WarningMark badge label="Incomplete" side="bottom" text={missingCloseText(holdings)} />
            ) : null}
          </>
        }
      >
        <MoneyOrDash value={totals.day_change} reason={NO_CLOSE} showPlus />
        <PercentText rate={totals.day_change_pct} showPlus className="stat__aside" />
      </Stat>

      {/* An investment account whose positions never came across still holds
          money, so the figure that includes it is shown whenever the two
          differ. */}
      {totals.account_balance_not_held !== 0 ? (
        <Stat label="Total value">
          <Money value={totals.total_value} tone="neutral" />
          <span className="stat__aside">
            incl. <Money value={totals.account_balance_not_held} tone="neutral" /> in cash
          </span>
        </Stat>
      ) : null}

      {/* Quotes older than four days are warned about, in the treatment the
          incomplete totals use. */}
      {asOf ? (
        asOf.stale ? (
          <WarningMark
            className="stat-row__aside"
            label={asOf.label}
            side="bottom"
            text={staleQuoteText(asOf)}
          />
        ) : (
          <span className="stat-row__aside">{asOf.label}</span>
        )
      ) : null}
    </div>
  )
}

/** The sortable column heads, in the order the table draws them. */
const COLUMNS: { key: HoldingSortKey; label: string; numeric: boolean }[] = [
  { key: 'symbol', label: 'Symbol', numeric: false },
  { key: 'name', label: 'Name', numeric: false },
  { key: 'shares', label: 'Shares', numeric: true },
  { key: 'price', label: 'Price', numeric: true },
  { key: 'market_value', label: 'Value', numeric: true },
  { key: 'share', label: 'Alloc %', numeric: true },
  { key: 'cost_basis', label: 'Total cost', numeric: true },
  { key: 'total_gain', label: 'Gain $', numeric: true },
  { key: 'day_change', label: 'Day $', numeric: true },
  { key: 'day_change_pct', label: 'Day %', numeric: true },
]

export function HoldingsTable({
  holdings,
  accountName,
  byAccount = false,
  isSmallBalance,
  onOpen,
  onRemove,
}: {
  holdings: readonly Holding[]
  /** Which account a position is in, for tickers held in more than one (several "SWEEP $1.00" rows). */
  accountName?: (id: string) => string
  /** Draw a subhead per account rather than one flat list. */
  byAccount?: boolean
  /** Accounts whose positions are left out behind a "small balances hidden" row; every figure still counts them. */
  isSmallBalance?: (accountId: string) => boolean
  /** Open the security's own screen. The symbol is the affordance. */
  onOpen?: (holding: Holding) => void
  onRemove?: (holding: Holding) => void
}) {
  const [sort, setSort] = useState<HoldingSort>(DEFAULT_SORT)
  const [revealed, setRevealed] = useState(false)
  const { shown, hidden } = withoutSmallBalances(
    holdings,
    (holding) => isSmallBalance?.(holding.account_id) ?? false,
    revealed,
  )
  const hasActions = onRemove !== undefined
  const columns = COLUMNS.length + (hasActions ? 1 : 0)
  // Named only where the name distinguishes: a ticker held once is already
  // unambiguous, and an account under every row is noise on most of them.
  const heldTwice = new Set(
    shown
      .map((one) => one.symbol)
      .filter((symbol, i, all) => all.indexOf(symbol) !== i),
  )

  const ordered = sortHoldings(shown, sort)
  const groups = byAccount
    ? groupHoldingsByAccount(ordered, accountName ?? accountNamer([]))
    : [{ accountId: 'all', label: '', rows: ordered }]

  const row = (holding: Holding) => (
            <tr key={holding.id}>
              <Td label="" className="stack-lead">
                {onOpen ? (
                  <button
                    type="button"
                    className="holding__open"
                    onClick={() => onOpen(holding)}
                    aria-label={`Open ${holding.symbol}`}
                  >
                    <Badge>{holding.symbol}</Badge>
                  </button>
                ) : (
                  <Badge>{holding.symbol}</Badge>
                )}
              </Td>
              {/* A floor on the name column, so on a tablet the row scrolls
                  rather than wrapping the name a word per line. */}
              <Td label="" className="stack-lead holding__name">
                <span className="cell__clip" title={holding.name}>
                  {holding.name}
                </span>
                {heldTwice.has(holding.symbol) && accountName?.(holding.account_id) ? (
                  <span className="cell__sub">
                    {accountName(holding.account_id)}
                  </span>
                ) : null}
              </Td>
              <Td numeric className="stack-inline">
                {formatShares(holding.shares)}
              </Td>
              <Td numeric className="stack-inline">
                {holding.is_unquoted ? (
                  <span className="money money--absent" title={NO_QUOTE}>
                    {EM_DASH}
                  </span>
                ) : (
                  <PriceText price={holding.price} currency={holding.currency} />
                )}
              </Td>
              <Td numeric className="stack-inline">
                <Money value={holding.market_value} tone="neutral" />
              </Td>
              <Td numeric className="stack-inline">
                <PercentText
                  rate={fractionToPercentUnits(parseRate(holding.share))}
                  tone="neutral"
                  digits={1}
                />
              </Td>
              <Td numeric className="stack-inline">
                {holding.is_cost_basis_complete ? (
                  <MoneyOrDash value={holding.cost_basis} reason={NO_BASIS} tone="neutral" />
                ) : (
                  <WarningMark
                    badge
                    side="left"
                    text={holdingMissingBasisText(holding)}
                  />
                )}
              </Td>
              <Td numeric className="stack-inline">
                <MoneyOrDash value={holding.total_gain} reason={NO_BASIS} showPlus />
              </Td>
              <Td numeric className="stack-inline">
                <MoneyOrDash value={holding.day_change} reason={NO_CLOSE} showPlus />
              </Td>
              <Td numeric className="stack-inline">
                <PercentText rate={holding.day_change_pct} showPlus />
              </Td>
              {hasActions ? (
                <Td numeric label="" className="stack-corner">
                  {onRemove ? (
                    <RowActions>
                      <OverflowMenu
                        label={`Actions for ${holding.symbol}`}
                        actions={[
                          <AskMenuItem subject={() => holdingSubject(holding)} />,
                          {
                            label: 'Remove holding',
                            icon: <Trash2 size={14} />,
                            danger: true,
                            onSelect: () => onRemove(holding),
                          },
                        ]}
                      />
                    </RowActions>
                  ) : null}
                </Td>
              ) : null}
            </tr>
  )

  return (
    <Table
      density="sm"
      lines={heldTwice.size > 0 && accountName !== undefined ? 2 : 1}
      stack
      className="holdings"
    >
      <thead>
        <tr>
          {COLUMNS.map((column) => (
            <SortableTh
              key={column.key}
              label={column.label}
              numeric={column.numeric}
              direction={sort.key === column.key ? sort.direction : null}
              onSort={() => setSort(nextSort(sort, column.key))}
            />
          ))}
          {hasActions ? <Th /> : null}
        </tr>
      </thead>
      <tbody>
        {holdings.length === 0 ? (
          <TableEmptyRow colSpan={columns}>No holdings yet. They arrive when a brokerage syncs, or add one by hand.</TableEmptyRow>
        ) : (
          groups.map((group) => (
            <Fragment key={group.accountId}>
              {byAccount ? (
                <TableGroupRow colSpan={columns}>{group.label}</TableGroupRow>
              ) : null}
              {group.rows.map(row)}
            </Fragment>
          ))
        )}
        <SmallBalancesRow
          colSpan={columns}
          hidden={hidden}
          revealed={revealed}
          onToggle={() => setRevealed(!revealed)}
        />
      </tbody>
    </Table>
  )
}
