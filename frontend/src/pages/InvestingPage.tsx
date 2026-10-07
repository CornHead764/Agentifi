import { clsx } from 'clsx'
import {
  ChartArea,
  ChartColumn,
  Check,
  ChevronDown,
  Layers,
  Plus,
  RefreshCw,
  Upload,
} from 'lucide-react'
import { Fragment, useMemo, useState } from 'react'

import {
  AreaTrend,
  BarSeries,
  MultiLine,
  StackedAreas,
  labeledAxis,
  labeledPoints,
} from '@/components/charts'
import { AskRowButton } from '@/components/assistant/AskMenuItem'
import { seriesColor } from '@/components/charts/palette'
import { MoneyOrDash, PercentText, Stat } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { RangeChips } from '@/components/RangeChips'
import { withoutSmallBalances } from '@/components/shell/accountTree'
import { NewAccountDialog } from '@/components/shell/NewAccountDialog'
import { SmallBalancesRow } from '@/components/shell/SmallBalancesRow'
import { accountNamer, openInvestmentAccounts, smallBalanceAccounts } from '@/lib/accounts'
import { usePreferredRange } from '@/lib/defaultRange'
import {
  Badge,
  Button,
  Card,
  ChipGroup,
  ConfirmDialog,
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
  IconButton,
  PageHeader,
  SearchInput,
  Spinner,
  Table,
  TableEmptyRow,
  TableGroupRow,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Td,
  Th,
  useConfirm,
  useProgressToast,
} from '@/components/ui'
import { portfolioSubject } from '@/lib/assistant/subjects'
import { rangeChangeLabel, type RangePreset } from '@/lib/dateRanges'
import {
  accountDayChange,
  accountMarketValue,
  useAccountPerformance,
  useInvestmentActivity,
  usePerformance,
  usePortfolio,
  useRefreshPrices,
  useRemoveHolding,
  useSecurities,
  type ActivityKind,
  type ActivityRow,
  type Holding,
  type PerformanceMetric,
} from '@/lib/clients/investments'
import {
  formatDate,
  fractionToPercentUnits,
  parseRate,
 } from '@/lib/format'
import { moneyToNumber, subMoney, type Money as MoneyValue } from '@/lib/money'
import { matchesSearch } from '@/lib/search'
import { useAccounts } from '@/lib/transactions/queries'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { toggled } from '@/lib/toggle'

import { activityFilters, activityLabel, filterActivity } from './investing/activityKinds'
import { AddHoldingDialog } from './investing/AddHoldingDialog'
import { AllocationCard } from './investing/AllocationCard'
import { HoldingDetailDialog } from './investing/HoldingDetail'
import { HoldingsTable, PortfolioHeader } from './investing/HoldingsTable'
import { priceFreshness } from './investing/priceFreshness'
import { RelatedNews } from './investing/RelatedNews'
import { ValueHistoryDialog } from './settings/ValueHistoryDialog'

const RANGES: readonly RangePreset[] = ['1M', '3M', '6M', 'YTD', '1Y', '5Y']

/**
 * Investments: four tabs over one account selector.
 *
 * - **Incomplete cost basis is a first-class state**; see
 *   `investing/HoldingsTable`.
 * - **TWR and IRR are a toggle, not a decision.** They answer different
 *   questions and `calculations.md` §10 says "Do not pick one". `/performance`
 *   returns both, so the toggle reads one rather than refetching over what
 *   could be another window.
 * - **Small balances are a display rule.** The account tables and the
 *   holdings leave out an account the server flagged `hidden_small_balance`
 *   behind a line that reveals it; the selector, the charts, the allocation
 *   and every total still include it.
 */
export function InvestingPage() {
  const [selected, setSelected] = useState<string[] | null>(null)
  const accounts = useAccounts()

  const options = useMemo(
    () => (accounts.data ?? []).filter((account) => account.kind === 'investment'),
    [accounts.data],
  )
  const chosen = selected ?? options.map((account) => account.id)
  const [addingHolding, setAddingHolding] = useState(false)

  return (
    <div className="page page--wide">
      <Tabs defaultValue="portfolio">
        <PageHeader
          tabs={
            <TabsList>
              <TabsTrigger value="portfolio">Portfolio</TabsTrigger>
              <TabsTrigger value="balances">Balances</TabsTrigger>
              <TabsTrigger value="performance">Performance</TabsTrigger>
              <TabsTrigger value="transactions">Transactions</TabsTrigger>
            </TabsList>
          }
          actions={
            <Button variant="primary" size="sm" onClick={() => setAddingHolding(true)}>
              <Plus size={13} /> New holding
            </Button>
          }
        />

        <TabsContent value="portfolio" className="stack">
          <PortfolioTab selected={selected} onSelected={setSelected} options={options} />
        </TabsContent>
        <TabsContent value="balances" className="stack">
          <BalancesTab
            selected={selected}
            onSelected={setSelected}
            options={options}
            chosen={chosen}
          />
        </TabsContent>
        <TabsContent value="performance" className="stack">
          <PerformanceTab
            selected={selected}
            onSelected={setSelected}
            options={options}
            chosen={chosen}
          />
        </TabsContent>
        <TabsContent value="transactions" className="stack">
          <TransactionsTab selected={selected} onSelected={setSelected} options={options} />
        </TabsContent>
      </Tabs>

      <AddHoldingDialog
        open={addingHolding}
        onOpenChange={setAddingHolding}
        accounts={openInvestmentAccounts(accounts.data ?? [])}
      />
    </div>
  )
}

interface SelectorProps {
  selected: string[] | null
  onSelected: (ids: string[] | null) => void
  options: readonly AccountWithBalances[]
}

/**
 * An account table's rows: the chosen accounts less the small balances, each
 * keeping its place in `chosen`, which is its series in the chart above.
 */
function useAccountRows(chosen: readonly string[], options: readonly AccountWithBalances[]) {
  const [revealed, setRevealed] = useState(false)
  const isSmall = useMemo(() => smallBalanceAccounts(options), [options])
  const { shown, hidden } = withoutSmallBalances(
    chosen.map((id, index) => ({ id, index })),
    (row) => isSmall(row.id),
    revealed,
  )
  return { shown, hidden, revealed, onToggle: () => setRevealed(!revealed) }
}

/**
 * The account selector, shared by all four tabs. `null` is every account and
 * is sent as an omitted parameter, which the endpoints read as "all"; an empty
 * list would mean no accounts.
 */
function AccountFilter({ selected, onSelected, options }: SelectorProps) {
  const label =
    selected === null ? 'All accounts' : `${selected.length} of ${options.length} selected`

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm">
          {label} <ChevronDown size={13} />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuLabel>Accounts</DropdownMenuLabel>
        {/* Not a checkbox: unticking "all" would name no accounts, which this
            filter has no state for. */}
        <DropdownMenuItem
          icon={selected === null ? <Check size={14} /> : undefined}
          disabled={selected === null}
          onSelect={() => onSelected(null)}
        >
          All accounts
        </DropdownMenuItem>
        {options.map((option) => (
          <DropdownMenuCheckboxItem
            key={option.id}
            checked={selected === null || selected.includes(option.id)}
            onCheckedChange={(checked) => {
              const current = selected ?? options.map((one) => one.id)
              onSelected(toggled(current, option.id, checked))
            }}
          >
            {option.name}
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function PortfolioTab({ selected, onSelected, options }: SelectorProps) {
  const [search, setSearch] = useState('')
  const [byAccount, setByAccount] = useState(false)
  const [opened, setOpened] = useState<Holding | null>(null)
  const portfolio = usePortfolio(selected)
  const securities = useSecurities()
  const accounts = useAccounts()

  // Which account a position is in, for the tickers held in several.
  const accountNameOf = useMemo(() => accountNamer(accounts.data), [accounts.data])
  const isSmallBalance = useMemo(() => smallBalanceAccounts(accounts.data), [accounts.data])

  const progress = useProgressToast()
  const removeHolding = useConfirm(useRemoveHolding(), {
    variables: (holding: Holding) => holding.id,
  })
  const refresh = useRefreshPrices()
  const refreshPrices = () =>
    void progress(
      'Refreshing prices…',
      refresh.mutateAsync(),
      (result) => ({
        title: result.updated === 0 ? 'No prices moved' : `Re-quoted ${result.updated} securities`,
        description:
          result.unpriced.length === 0
            ? 'Every symbol answered.'
            : `No quote for ${result.unpriced.join(', ')}: stored prices kept.`,
        tone: result.updated === 0 && result.unpriced.length > 0 ? 'error' : 'success',
      }),
      'Prices were not refreshed',
    )

  const asOf = useMemo(
    () => priceFreshness((securities.data ?? []).map((one) => one.last_price_at)),
    [securities.data],
  )

  const filtered = (portfolio.data?.items ?? []).filter((holding) =>
    matchesSearch(search, holding.symbol, holding.name),
  )

  return (
    <>
      <Card>
        <div className="toolbar toolbar--flush">
          <AccountFilter selected={selected} onSelected={onSelected} options={options} />
          <span className="toolbar__spacer" />
          <AskRowButton subject={portfolioSubject} variant="secondary" />
          <IconRefresh
            busy={refresh.isPending}
            onClick={() => (refresh.isPending ? undefined : refreshPrices())}
          />
        </div>
        <QueryBoundary query={portfolio} rows={3}>
          {(data) => <PortfolioHeader totals={data.totals} holdings={data.items} asOf={asOf} />}
        </QueryBoundary>
      </Card>

      <QueryBoundary query={portfolio} rows={4}>
        {(data) => (
          <AllocationCard
            allocation={data.allocation}
            byClass={data.allocation_by_class}
            byAccount={data.allocation_by_account}
          />
        )}
      </QueryBoundary>

      <RelatedNews />

      <Card
        title="Holdings"
        flush
        actions={
          <>
            <SearchInput
              size="sm"
              placeholder="Search holdings"
              value={search}
              onChange={setSearch}
              aria-label="Search holdings"
            />
            {/* One flat list by default, with a subhead per account on request. */}
            <Button size="sm" aria-pressed={byAccount} onClick={() => setByAccount(!byAccount)}>
              <Layers size={13} /> {byAccount ? 'Flat' : 'Group'}
            </Button>
          </>
        }
      >
        <QueryBoundary query={portfolio} rows={8}>
          {() => (
            <HoldingsTable
              holdings={filtered}
              accountName={accountNameOf}
              byAccount={byAccount}
              isSmallBalance={isSmallBalance}
              onOpen={setOpened}
              onRemove={removeHolding.ask}
            />
          )}
        </QueryBoundary>
      </Card>

      <HoldingDetailDialog
        securityId={opened?.security_id ?? null}
        symbol={opened?.symbol ?? ''}
        accountName={accountNameOf}
        onClose={() => setOpened(null)}
      />

      <ConfirmDialog
        {...removeHolding.dialog}
        title={`Remove ${removeHolding.target?.symbol ?? 'this holding'}?`}
        description="Removes the position and its lots. Account transactions are kept."
        confirmLabel="Remove holding"
      />
    </>
  )
}

function IconRefresh({ onClick, busy = false }: { onClick: () => void; busy?: boolean }) {
  return (
    <Button size="sm" onClick={onClick} disabled={busy}>
      {busy ? <Spinner size={13} /> : <RefreshCw size={13} />} {busy ? 'Refreshing…' : 'Refresh'}
    </Button>
  )
}

/**
 * Balances. The chart and headline come from the portfolio's `/performance`
 * series, the per-account rows from one call per account over the same
 * resolved window. Day $ and Day % are aggregated from `/holdings`, and are a
 * dash for an account where any position has no prior close.
 */
function BalancesTab({
  selected,
  onSelected,
  options,
  chosen,
}: SelectorProps & { chosen: readonly string[] }) {
  const [range, setRange] = usePreferredRange('1M', RANGES)
  const series = usePerformance(range, selected)
  const perAccount = useAccountPerformance(range, [...chosen])
  const portfolio = usePortfolio(selected)
  const [addingAccount, setAddingAccount] = useState(false)
  const [historyAccount, setHistoryAccount] = useState<AccountWithBalances | null>(null)
  // Simplifi's chart toggles: the total as an area or bars, or
  // every account stacked so the total reads as its parts.
  const [chart, setChart] = useState<'area' | 'stacked' | 'bars'>('area')
  const accountName = useMemo(() => accountNamer(options), [options])
  const rows = useAccountRows(chosen, options)
  const axis = useMemo(
    () =>
      [...new Set((series.data?.points ?? []).map((point) => point.on))]
        .sort()
        .map((on) => ({ key: on, label: on })),
    [series.data],
  )

  return (
    <>
      <Card>
        <div className="toolbar toolbar--flush">
          <AccountFilter selected={selected} onSelected={onSelected} options={options} />
          <RangeChips value={range} onChange={setRange} presets={RANGES} />
          <span className="toolbar__spacer" />
          <span className="chart-toggle" role="group" aria-label="Chart type">
            <IconButton
              label="Balance as an area"
              variant="ghost"
              size="sm"
              aria-pressed={chart === 'area'}
              onClick={() => setChart('area')}
            >
              <ChartArea size={14} />
            </IconButton>
            <IconButton
              label="Balance as bars"
              variant="ghost"
              size="sm"
              aria-pressed={chart === 'bars'}
              onClick={() => setChart('bars')}
            >
              <ChartColumn size={14} />
            </IconButton>
            <IconButton
              label="Stacked by account"
              variant="ghost"
              size="sm"
              aria-pressed={chart === 'stacked'}
              onClick={() => setChart('stacked')}
            >
              <Layers size={14} />
            </IconButton>
          </span>
          <ImportHistoryControl chosen={chosen} options={options} onOpen={setHistoryAccount} />
          <Button variant="primary" size="sm" onClick={() => setAddingAccount(true)}>
            <Plus size={13} /> New account
          </Button>
        </div>

        <QueryBoundary query={series} rows={6}>
          {(data) => (
            <>
              <div className="stat-row">
                <Stat label="Current balance">
                  <Money value={data.end_value} tone="neutral" />
                </Stat>
                <Stat label={rangeChangeLabel(range)}>
                  <Money value={subMoney(data.end_value, data.start_value)} showPlus />
                  <PercentText
                    rate={data.points.at(-1)?.return_pct ?? null}
                    showPlus
                    className="stat__aside"
                  />
                </Stat>
              </div>
              {chart === 'area' ? (
                <AreaTrend points={labeledPoints(data.points, (point) => point.value)} />
              ) : chart === 'bars' ? (
                <BarSeries
                  points={labeledPoints(data.points, (point) => point.value)}
                  height={280}
                />
              ) : (
                <StackedAreas
                  axis={axis}
                  series={chosen.map((id, index) => ({
                    key: id,
                    label: accountName(id),
                    color: seriesColor(index),
                    points: Object.fromEntries(
                      (perAccount[index]?.data?.points ?? []).map((point) => [
                        point.on,
                        moneyToNumber(point.value),
                      ]),
                    ),
                  }))}
                />
              )}
            </>
          )}
        </QueryBoundary>
      </Card>

      <Card title="Accounts" flush>
        <Table density="sm">
          <thead>
            <tr>
              <Th>Account</Th>
              <Th numeric>Day $</Th>
              <Th numeric>Day %</Th>
              <Th numeric>{rangeChangeLabel(range).replace(' change', '')} $</Th>
              <Th numeric>{rangeChangeLabel(range).replace(' change', '')} %</Th>
              <Th numeric>Current balance</Th>
            </tr>
          </thead>
          <tbody>
            {chosen.length === 0 ? (
              <TableEmptyRow colSpan={6}>{noneChosen(options.length)}</TableEmptyRow>
            ) : (
              rows.shown.map(({ id, index }) => {
                const window = perAccount[index]?.data
                const holdings = portfolio.data?.items ?? []
                const day = accountDayChange(holdings, id)
                return (
                  <tr key={id}>
                    <Td>
                      <span className="dot" style={{ background: seriesColor(index) }} />
                      {accountName(id)}
                    </Td>
                    <Td numeric>
                      <MoneyOrDash value={day} showPlus reason="No prior close on file." />
                    </Td>
                    <Td numeric>
                      <PercentText rate={fractionToPercentUnits(dayRate(day, accountMarketValue(holdings, id)))} showPlus />
                    </Td>
                    <Td numeric>
                      <MoneyOrDash
                        value={
                          window ? subMoney(window.end_value, window.start_value) : null
                        }
                        showPlus
                        reason="No balance recorded at the start of this window."
                      />
                    </Td>
                    <Td numeric>
                      <PercentText rate={window?.points.at(-1)?.return_pct ?? null} showPlus />
                    </Td>
                    <Td numeric>
                      {window ? <Money value={window.end_value} tone="neutral" /> : null}
                    </Td>
                  </tr>
                )
              })
            )}
            <SmallBalancesRow
              colSpan={6}
              hidden={rows.hidden}
              revealed={rows.revealed}
              onToggle={rows.onToggle}
            />
          </tbody>
        </Table>
      </Card>

      <NewAccountDialog open={addingAccount} onOpenChange={setAddingAccount} />
      {historyAccount ? (
        <ValueHistoryDialog
          account={historyAccount}
          open
          onOpenChange={(open) => {
            if (!open) setHistoryAccount(null)
          }}
        />
      ) : null}
    </>
  )
}

/**
 * The Import History button. The dialog takes one account: with one selected
 * it opens straight to it, with several it asks which first.
 */
export function ImportHistoryControl({
  chosen,
  options,
  onOpen,
}: {
  chosen: readonly string[]
  options: readonly AccountWithBalances[]
  onOpen: (account: AccountWithBalances) => void
}) {
  const label = (
    <>
      <Upload size={13} /> Import History
    </>
  )

  // Nothing ticked in the filter is not "import into nothing": every account
  // on offer is still a place the history could go.
  const pool = chosen.length > 0 ? chosen : options.map((one) => one.id)

  if (pool.length === 0) {
    return (
      <Button size="sm" disabled>
        {label}
      </Button>
    )
  }

  if (pool.length === 1) {
    const account = options.find((one) => one.id === pool[0])
    return (
      <Button size="sm" disabled={!account} onClick={() => account && onOpen(account)}>
        {label}
      </Button>
    )
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm">{label}</Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel>Import history for</DropdownMenuLabel>
        {pool.map((id) => {
          const account = options.find((one) => one.id === id)
          return account ? (
            <DropdownMenuItem key={id} onSelect={() => onOpen(account)}>
              {account.name}
            </DropdownMenuItem>
          ) : null
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * `day_change / (market_value − day_change)` — §10's own denominator, which is
 * yesterday's close rather than today's value. Unknown in, unknown out.
 */
function dayRate(day: MoneyValue | null, marketValue: MoneyValue): number | null {
  if (day === null) return null
  const prior = marketValue - day
  return prior === 0 ? null : day / Math.abs(prior)
}

function PerformanceTab({
  selected,
  onSelected,
  options,
  chosen,
}: SelectorProps & { chosen: readonly string[] }) {
  const [range, setRange] = usePreferredRange('1M', RANGES)
  const [metric, setMetric] = useState<PerformanceMetric>('twr')
  const overall = usePerformance(range, selected)
  const perAccount = useAccountPerformance(range, [...chosen])
  const accountName = useMemo(() => accountNamer(options), [options])
  const rows = useAccountRows(chosen, options)

  const axis = useMemo(
    () => [...new Set((overall.data?.points ?? []).map((point) => point.on))].sort(),
    [overall.data],
  )

  return (
    <>
      <Card>
        <div className="toolbar toolbar--flush">
          <AccountFilter selected={selected} onSelected={onSelected} options={options} />
          <RangeChips value={range} onChange={setRange} presets={RANGES} />
          <span className="toolbar__spacer" />
          <MetricToggle metric={metric} onMetric={setMetric} />
        </div>

        <QueryBoundary query={overall} rows={8}>
          {(data) => (
            <>
              <div className="stat-row">
                <Stat label={metric === 'twr' ? 'Time-weighted' : 'Money-weighted'}>
                  <PercentText rate={metric === 'twr' ? data.twr_pct : data.irr_pct} showPlus />
                </Stat>
                <Stat label={rangeChangeLabel(range)}>
                  <PercentText rate={data.points.at(-1)?.return_pct ?? null} showPlus />
                </Stat>
                <Stat label="Net deposits">
                  <Money value={data.net_flows} showPlus />
                </Stat>
              </div>

              <MultiLine
                unit="percent"
                axis={labeledAxis(axis)}
                series={chosen.map((id, index) => ({
                  key: id,
                  label: accountName(id),
                  color: seriesColor(index),
                  // A rate that did not converge is a gap in the line, never a
                  // zero: IRR returns no answer rather than a wrong root.
                  points: Object.fromEntries(
                    (perAccount[index]?.data?.points ?? []).map((point) => [
                      point.on,
                      parseRate(point.return_pct),
                    ]),
                  ),
                }))}
              />
            </>
          )}
        </QueryBoundary>
      </Card>

      <Card title="Accounts" flush>
        <Table density="sm">
          <thead>
            <tr>
              <Th>Account</Th>
              <Th numeric>TWR %</Th>
              <Th numeric>IRR %</Th>
              <Th numeric>Balance today</Th>
            </tr>
          </thead>
          <tbody>
            {chosen.length === 0 ? (
              <TableEmptyRow colSpan={4}>{noneChosen(options.length)}</TableEmptyRow>
            ) : (
              rows.shown.map(({ id, index }) => {
                const window = perAccount[index]?.data
                return (
                  <tr key={id}>
                    <Td>
                      <span className="dot" style={{ background: seriesColor(index) }} />
                      {accountName(id)}
                    </Td>
                    <Td numeric>
                      <PercentText rate={window?.twr_pct ?? null} showPlus />
                    </Td>
                    <Td numeric>
                      <PercentText rate={window?.irr_pct ?? null} showPlus />
                    </Td>
                    <Td numeric>
                      {window ? <Money value={window.end_value} tone="neutral" /> : null}
                    </Td>
                  </tr>
                )
              })
            )}
            <SmallBalancesRow
              colSpan={4}
              hidden={rows.hidden}
              revealed={rows.revealed}
              onToggle={rows.onToggle}
            />
          </tbody>
        </Table>
      </Card>
    </>
  )
}

function MetricToggle({
  metric,
  onMetric,
}: {
  metric: PerformanceMetric
  onMetric: (next: PerformanceMetric) => void
}) {
  return (
    <ChipGroup
      label="Return measure"
      layout="tight"
      value={metric}
      onChange={onMetric}
      options={[
        {
          value: 'twr',
          label: 'TWR',
          hint: 'Time-weighted: strips out deposits, so it measures the investments.',
        },
        {
          value: 'irr',
          label: 'IRR',
          hint: 'Money-weighted: weighted by amount invested, so it measures the investor.',
        },
      ]}
    />
  )
}

/**
 * Transaction activity: the register's rows for the investment accounts, each
 * classified by `/investment-activity` from the bank's wording.
 *
 * No quantity column, because no row stores one. A row nothing recognized
 * shows no action, since picking one would be a guess beside a real amount.
 */
function TransactionsTab({ selected, onSelected, options }: SelectorProps) {
  const [range, setRange] = usePreferredRange('1Y', RANGES)
  const [kinds, setKinds] = useState<ActivityKind[]>([])
  const [search, setSearch] = useState('')
  const activity = useInvestmentActivity(range, selected)

  const accountName = useMemo(() => accountNamer(options), [options])

  const toggle = (kind: ActivityKind) =>
    setKinds(toggled(kinds, kind))

  return (
    <>
      <Card>
        <div className="toolbar toolbar--flush">
          <AccountFilter selected={selected} onSelected={onSelected} options={options} />
          <RangeChips value={range} onChange={setRange} presets={RANGES} />
          <span className="toolbar__spacer" />
          <SearchInput
            size="sm"
            placeholder="Search activity"
            value={search}
            onChange={setSearch}
            aria-label="Search activity"
          />
        </div>

        <QueryBoundary query={activity} rows={3}>
          {(data) => (
            <>
              <div className="stat-row">
                <Stat label="Income">
                  <Money value={data.income} showPlus />
                  <span className="stat__aside">dividends, interest, reinvestments</span>
                </Stat>
                <Stat label="Fees">
                  <Money value={data.fees} signs="absolute" tone="neutral" />
                </Stat>
              </div>
              {/* Chips for what the window holds, not for the nine kinds that
                  exist: a year with no fees offers no Fee chip to press. */}
              <div className="chips chips--wrap" role="group" aria-label="Kinds of activity">
                {activityFilters(data.summary).map((chip) => (
                  <button
                    key={chip.kind}
                    type="button"
                    aria-pressed={kinds.includes(chip.kind)}
                    className={clsx('chip', kinds.includes(chip.kind) && 'chip--on')}
                    onClick={() => toggle(chip.kind)}
                  >
                    {chip.label} <span className="muted">{chip.count}</span>
                  </button>
                ))}
              </div>
            </>
          )}
        </QueryBoundary>
      </Card>

      <Card title="Transaction activity" flush>
        <QueryBoundary query={activity} rows={10}>
          {(data) => (
            <ActivityRows
              rows={filterActivity(data.items, kinds, search)}
              accountName={accountName}
            />
          )}
        </QueryBoundary>
      </Card>
    </>
  )
}

function ActivityRows({
  rows,
  accountName,
}: {
  rows: readonly ActivityRow[]
  accountName: (id: string) => string
}) {
  const months = groupByMonth(rows)

  if (rows.length === 0) {
    return (
      <Table density="sm">
        <tbody>
          <TableEmptyRow colSpan={5}>No activity in these accounts.</TableEmptyRow>
        </tbody>
      </Table>
    )
  }

  return (
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
        {months.map(([month, entries]) => (
          <Fragment key={month}>
            <TableGroupRow colSpan={5}>{formatDate(`${month}-01`, 'monthLong')}</TableGroupRow>
            {entries.map((row) => (
              <tr key={row.transaction_id}>
                <Td label="" className="stack-lead nowrap">
                  {formatDate(row.on)}
                </Td>
                <Td>
                  {activityLabel(row.kind) === null ? null : (
                    <Badge>{activityLabel(row.kind)}</Badge>
                  )}
                </Td>
                <Td>
                  <span className="cell__clip" title={accountName(row.account_id)}>
                    {accountName(row.account_id)}
                  </span>
                </Td>
                <Td>
                  <span className="cell__clip" title={row.payee || row.statement_name}>
                    {row.payee || row.statement_name}
                    {row.is_pending ? <span className="muted"> pending</span> : null}
                  </span>
                </Td>
                <Td numeric className="stack-inline">
                  <Money value={row.amount} />
                </Td>
              </tr>
            ))}
          </Fragment>
        ))}
      </tbody>
    </Table>
  )
}

/** Newest month first; the rows arrive in date order inside each. */
function groupByMonth(rows: readonly ActivityRow[]): [string, ActivityRow[]][] {
  const months = new Map<string, ActivityRow[]>()
  for (const row of rows) {
    const key = row.on.slice(0, 7)
    const bucket = months.get(key)
    if (bucket) bucket.push(row)
    else months.set(key, [row])
  }
  return [...months.entries()].sort((a, b) => b[0].localeCompare(a[0]))
}

/** Why an account table is empty: nothing picked, or nothing to pick. */
function noneChosen(available: number): string {
  return available === 0
    ? 'No investment accounts. Connect one through SimpleFIN, or add one by hand.'
    : 'No investment accounts selected.'
}
