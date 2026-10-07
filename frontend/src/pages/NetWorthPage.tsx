import { ChartNoAxesCombined, ChevronDown, ChevronRight, Info } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'

import {
  AreaTrend,
  Donut,
  DonutGroup,
  DonutLegendRow,
  DrillTrail,
  MultiLine,
  labeledAxis,
  labeledPoints,
  type DonutSlice,
} from '@/components/charts'
import { seriesColor } from '@/components/charts/palette'
import { ChangeBadge, PercentText } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { AskRowButton } from '@/components/assistant/AskMenuItem'
import { RangeChips } from '@/components/RangeChips'
import { usePreferredRange } from '@/lib/defaultRange'
import { nodeTotal, treeTotal, type AccountNode } from '@/components/shell/accountTree'
import {
  Badge,
  Button,
  Callout,
  Card,
  List,
  ListRow,
  ChipGroup,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
  EmptyState,
  Meter,
  OverflowMenu,
  PageHeader,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui'
import { rangeChangeLabel } from '@/lib/dateRanges'
import {
  amountOn,
  groupLabel,
  groupsOn,
  kindLabel,
  kindsOn,
  useNetWorth,
  type NetWorth,
  type NetWorthGroup,
  type NetWorthPoint,
} from '@/lib/clients/networth'
import { netWorthSubject } from '@/lib/assistant/subjects'
import { useAccounts } from '@/lib/transactions/queries'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { ValueHistoryDialog } from '@/pages/settings/ValueHistoryDialog'
import {
  changeFraction,
  formatPercent,
  fractionToPercentUnits,
  parseRate,
  plural,
  shareOf,
} from '@/lib/format'
import { absMoney, moneyToNumber, subMoney, sumMoney, type Money as MoneyValue } from '@/lib/money'
import { registerLinkForAccount } from '@/lib/transactions/links'

import { debtBand, debtMarkerPosition } from './networth/debtRatio'

/**
 * Which line the chart draws. Every sampled day carries `by_kind`, so the
 * by-type views are real history rather than closing balances repeated flat.
 * A view with `line` is one filled trend; the rest compare several lines.
 */
type GraphView = 'total' | 'equity' | 'assets_vs_debt' | 'assets_by_type' | 'debt_by_type'

const GRAPH_VIEWS: Record<GraphView, { label: string; line?: (point: NetWorthPoint) => MoneyValue }> = {
  total: { label: 'Total net worth', line: (point) => point.net },
  // A household with no secured loans draws the same line as Assets — that
  // is `EquityAt` with nothing to subtract, not a case this view special-cases.
  equity: { label: 'Equity', line: (point) => point.equity },
  assets_vs_debt: { label: 'Assets vs. debt' },
  assets_by_type: { label: 'Assets by type' },
  debt_by_type: { label: 'Debt by type' },
}

const GRAPH_VIEW_KEYS: readonly GraphView[] = [
  'total',
  'equity',
  'assets_vs_debt',
  'assets_by_type',
  'debt_by_type',
]

function readGraphView(value: string): GraphView {
  return GRAPH_VIEW_KEYS.find((one) => one === value) ?? 'total'
}

/**
 * Net Worth, full width: the page has its own accounts panel, and two account
 * lists on one screen disagree.
 *
 * One request, `/net-worth`, backs the whole screen, so the chart and the
 * panel cannot be computed over different windows (`calculations.md` §4).
 * **A group row's change is over the selected window, and its percentage is
 * relative to the window start.** Against a zero start it is an em dash.
 */
export function NetWorthPage() {
  const [range, setRange] = usePreferredRange('6M')
  const [view, setView] = useState<GraphView>('total')
  const [startAtZero, setStartAtZero] = useState(false)
  const [historyFor, setHistoryFor] = useState<AccountWithBalances | null>(null)

  const worth = useNetWorth(range)
  const allAccounts = useAccounts()
  const openAccounts = useMemo(
    () => (allAccounts.data ?? []).filter((account) => !account.is_closed),
    [allAccounts.data],
  )
  const heldIds = useMemo(
    () =>
      new Set(
        (allAccounts.data ?? [])
          .filter((account) => account.withheld_balance_at !== null)
          .map((account) => account.id),
      ),
    [allAccounts.data],
  )

  return (
    <div className="page page--wide">
      <PageHeader
        actions={
          <span className="toolbar__note">
            <Info size={13} aria-hidden="true" />
            {worth.data
              ? `${worth.data.included_accounts} / ${worth.data.total_accounts} accounts included`
              : 'Accounts included'}
          </span>
        }
      >
        <RangeChips value={range} onChange={setRange} />
      </PageHeader>

      {/* When a currency cannot be converted, the page says so rather than
          presenting the sum as exact. */}
      {worth.data && worth.data.unconverted_currencies.length > 0 ? (
        <Callout tone="warning">
          No exchange rate for {worth.data.unconverted_currencies.join(', ')}: counted at face
          value.
        </Callout>
      ) : null}

      <Card
        title="Your net worth"
        actions={
          <>
            <AskRowButton subject={netWorthSubject} variant="secondary" />
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button size="sm">
                  {GRAPH_VIEWS[view].label} <ChevronDown size={13} />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuLabel>Graph view</DropdownMenuLabel>
                <DropdownMenuRadioGroup
                  value={view}
                  onValueChange={(next) => setView(readGraphView(next))}
                >
                  {GRAPH_VIEW_KEYS.map((key) => (
                    <DropdownMenuRadioItem key={key} value={key}>
                      {GRAPH_VIEWS[key].label}
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              </DropdownMenuContent>
            </DropdownMenu>
            <DisplayMenu
              startAtZero={startAtZero}
              onStartAtZero={setStartAtZero}
              accounts={openAccounts}
              onImportHistory={setHistoryFor}
            />
          </>
        }
      >
        <QueryBoundary query={worth} rows={6}>
          {(data) => (
            <>
              <div className="headline">
                <Money value={data.end.net} tone="neutral" className="figure--total" />
                <ChangeBadge
                  amount={data.change}
                  rate={data.change_pct}
                  caption={rangeChangeLabel(range).toLowerCase()}
                />
              </div>
              <NetWorthGraph
                series={data}
                view={view}
                startAtZero={startAtZero}
                accounts={openAccounts}
                onImportHistory={setHistoryFor}
              />
            </>
          )}
        </QueryBoundary>
      </Card>

      <div className="split">
        <Card title="Accounts" subtitle={rangeChangeLabel(range)} flush>
          <QueryBoundary query={worth} rows={8}>
            {(data) => (
              <>
                <GroupSection title="Assets" groups={groupsOn(data, 'asset')} heldIds={heldIds} />
                <GroupSection title="Debt" groups={groupsOn(data, 'debt')} heldIds={heldIds} />
              </>
            )}
          </QueryBoundary>
        </Card>

        <div className="stack">
          <Card title="Debt to asset ratio">
            <QueryBoundary query={worth} rows={3}>
              {(data) => <DebtToAsset ratio={parseRate(data.debt_to_asset)} />}
            </QueryBoundary>
          </Card>

          <Card title="Breakdown">
            <QueryBoundary query={worth} rows={6}>
              {(data) => <Breakdown worth={data} />}
            </QueryBoundary>
          </Card>
        </div>
      </div>

      {historyFor ? (
        <ValueHistoryDialog
          account={historyFor}
          open
          onOpenChange={(open) => {
            if (!open) setHistoryFor(null)
          }}
        />
      ) : null}
    </div>
  )
}

/**
 * A group and its accounts as the drawer's own node shape, so
 * `accountTree.nodeTotal` holds the one roll-up rule for both this panel and
 * the drawer on the same screen.
 */
function groupNodes(groups: readonly NetWorthGroup[]): AccountNode[] {
  return groups.map((group) => ({
    id: group.kind,
    name: groupLabel(group),
    kind: 'group',
    balance: group.end,
    children: group.accounts.map((account) => ({
      id: account.account_id,
      name: account.name,
      kind: 'account' as const,
      balance: account.end,
      closed: account.is_closed,
    })),
  }))
}

function DisplayMenu({
  startAtZero,
  onStartAtZero,
  accounts,
  onImportHistory,
}: {
  startAtZero: boolean
  onStartAtZero: (on: boolean) => void
  accounts: readonly AccountWithBalances[]
  onImportHistory: (account: AccountWithBalances) => void
}) {
  return (
    <OverflowMenu
      label="Actions for the net worth chart"
      actions={[]}
      sections={[
        {
          label: 'Display',
          entries: [
            { label: 'Always start chart at $0', checked: startAtZero, onCheckedChange: onStartAtZero },
          ],
        },
        {
          // Importing history is per account, so the item asks which one
          // and opens the import over this page.
          entries: [
            accounts.length > 0 && {
              label: 'Import historical balances',
              items: accounts.map((account) => ({
                label: account.name,
                onSelect: () => onImportHistory(account),
              })),
            },
          ],
        },
      ]}
    />
  )
}

/** "Import balance history" for the empty chart: which account, then the import. */
function ImportHistoryButton({
  accounts,
  onImportHistory,
}: {
  accounts: readonly AccountWithBalances[]
  onImportHistory: (account: AccountWithBalances) => void
}) {
  if (accounts.length === 0) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm">
          Import balance history <ChevronDown size={13} aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        <DropdownMenuLabel>Import history for</DropdownMenuLabel>
        {accounts.map((account) => (
          <DropdownMenuItem key={account.id} onSelect={() => onImportHistory(account)}>
            {account.name}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function NetWorthGraph({
  series,
  view,
  startAtZero,
  accounts,
  onImportHistory,
}: {
  series: NetWorth
  view: GraphView
  startAtZero: boolean
  accounts: readonly AccountWithBalances[]
  onImportHistory: (account: AccountWithBalances) => void
}) {
  // A window with nothing recorded in it would draw axes over blank space,
  // which reads as a chart that failed rather than a range with no history yet.
  if (series.points.length === 0) {
    return (
      <EmptyState
        icon={<ChartNoAxesCombined size={20} />}
        title="No balance history in this range"
        body="Pick a longer range, or import past balances."
        action={<ImportHistoryButton accounts={accounts} onImportHistory={onImportHistory} />}
      />
    )
  }

  const line = GRAPH_VIEWS[view].line
  if (line) {
    return <AreaTrend points={labeledPoints(series.points, line)} startAtZero={startAtZero} />
  }

  // One label per point, unique across the window: the axis resolves hover by
  // these values, and "Jan 31" repeats on every multi-year range.
  const axis = labeledAxis(series.points.map((point) => point.on))

  if (view === 'assets_by_type' || view === 'debt_by_type') {
    const side = view === 'assets_by_type' ? 'asset' : 'debt'
    return (
      <MultiLine
        axis={axis}
        series={kindsOn(series.points, side).map((kind, index) => ({
          key: kind,
          label: kindLabel(kind),
          color: seriesColor(index),
          points: byDate(series.points.map((point) => [point.on, amountOn(point, kind)])),
        }))}
        startAtZero={startAtZero}
      />
    )
  }

  // Debt arrives positive and stays so: the two lines are compared in size.
  return (
    <MultiLine
      axis={axis}
      series={[
        {
          key: 'assets',
          label: 'Assets',
          color: 'var(--income)',
          points: byDate(series.points.map((point) => [point.on, point.assets])),
        },
        {
          key: 'debt',
          label: 'Debt',
          color: 'var(--expense)',
          points: byDate(series.points.map((point) => [point.on, point.debt])),
        },
      ]}
      startAtZero={startAtZero}
    />
  )
}

/** Chart values are floats by design; `Money` stops at the plotting boundary. */
function byDate(pairs: readonly (readonly [string, MoneyValue])[]): Record<string, number> {
  const out: Record<string, number> = {}
  for (const [on, value] of pairs) out[on] = moneyToNumber(value)
  return out
}

function GroupSection({
  title,
  groups,
  heldIds,
}: {
  title: string
  groups: readonly NetWorthGroup[]
  heldIds: ReadonlySet<string>
}) {
  const [open, setOpen] = useState(true)
  const [openKinds, setOpenKinds] = useState<ReadonlySet<string>>(new Set())
  const nodes = useMemo(() => groupNodes(groups), [groups])

  const total = treeTotal(nodes)
  const start = sumMoney(groups.map((group) => group.start))
  const change = subMoney(total, start)
  // The same null rule as every other percentage on this page: a side that
  // opened the window at nothing has no baseline to have moved from.
  const rate = fractionToPercentUnits(changeFraction(start, total))

  const toggleKind = (kind: string) =>
    setOpenKinds((current) => {
      const next = new Set(current)
      if (next.has(kind)) next.delete(kind)
      else next.add(kind)
      return next
    })

  return (
    <List className="acct-panel">
      <ListRow
        className="acct-panel__side"
        title={
          <>
            <Chevron open={open} />
            {title}
          </>
        }
        figures={<Money value={total} tone="neutral" />}
        figuresSub={<ChangeBadge amount={change} rate={rate} size="sm" />}
        onSelect={() => setOpen((current) => !current)}
        expanded={open}
      />
      {open
        ? groups.flatMap((group, index) => {
            const shown = openKinds.has(group.kind)
            return [
              <ListRow
                key={group.kind}
                className="acct-panel__group"
                title={
                  <>
                    <Chevron open={shown} />
                    {groupLabel(group)}
                  </>
                }
                sub={plural(group.account_count, 'account')}
                figures={<Money value={nodeTotal(nodes[index])} tone="neutral" />}
                figuresSub={
                  <ChangeBadge amount={group.change} rate={group.change_pct} size="sm" />
                }
                onSelect={() => toggleKind(group.kind)}
                expanded={shown}
              />,
              ...(shown
                ? group.accounts.map((account) => (
                    <ListRow
                      key={account.account_id}
                      className="acct-panel__account"
                      title={account.name}
                      badge={
                        heldIds.has(account.account_id) || account.is_closed ? (
                          <>
                            {heldIds.has(account.account_id) ? <Badge tone="warning">Held</Badge> : null}
                            {account.is_closed ? <Badge>Closed</Badge> : null}
                          </>
                        ) : undefined
                      }
                      figures={<Money value={account.end} tone="neutral" />}
                      figuresSub={
                        <ChangeBadge amount={account.change} rate={account.change_pct} size="sm" />
                      }
                    />
                  ))
                : []),
            ]
          })
        : null}
    </List>
  )
}

function Chevron({ open }: { open: boolean }) {
  const Icon = open ? ChevronDown : ChevronRight
  return <Icon size={13} className="acct-panel__chevron" aria-hidden="true" />
}

function DebtToAsset({ ratio }: { ratio: number | null }) {
  const band = debtBand(ratio)

  if (!band) {
    return (
      <EmptyState compact title="No assets recorded, so no ratio yet." />
    )
  }

  return (
    <>
      <div className="row row--3 ratio">
        <span className="figure--stat">{formatPercent(ratio, { digits: 0 })}</span>
        <Badge tone={band.rating === 'excellent' || band.rating === 'good' ? 'income' : 'warning'}>
          {band.label}
        </Badge>
      </div>
      <Meter
        className="ratio__scale"
        label="Debt to asset ratio"
        value={debtMarkerPosition(ratio)}
        valueText={formatPercent(ratio, { digits: 0 })}
        segments={[{ value: 100, tone: 'scale' }]}
        marker={debtMarkerPosition(ratio)}
      />
      <p className="muted">{band.coaching}</p>
    </>
  )
}

function Breakdown({ worth }: { worth: NetWorth }) {
  const [asShare, setAsShare] = useState(false)

  return (
    <Tabs defaultValue="assets">
      <div className="breakdown__head">
        <TabsList>
          <TabsTrigger value="assets">Assets</TabsTrigger>
          <TabsTrigger value="debt">Debt</TabsTrigger>
        </TabsList>
        <ChipGroup
          label="Show as"
          layout="tight"
          value={asShare ? 'share' : 'amount'}
          options={[
            { value: 'amount', label: '$', ariaLabel: 'Amounts' },
            { value: 'share', label: '%', ariaLabel: 'Shares of the total' },
          ]}
          onChange={(shown) => setAsShare(shown === 'share')}
        />
      </div>

      <TabsContent value="assets">
        <BreakdownList
          groups={groupsOn(worth, 'asset')}
          asShare={asShare}
          caption="Total Assets"
        />
      </TabsContent>
      <TabsContent value="debt">
        <BreakdownList
          groups={groupsOn(worth, 'debt')}
          asShare={asShare}
          caption="Total Debt"
        />
      </TabsContent>
    </Tabs>
  )
}

/**
 * The assets or debt ring, with two levels: account kinds, then the accounts
 * inside one, which open their register. A kind with a single account skips
 * the middle step. The total above follows the level.
 */
function BreakdownList({
  groups,
  asShare,
  caption,
}: {
  groups: readonly NetWorthGroup[]
  asShare: boolean
  caption: string
}) {
  const navigate = useNavigate()
  const [openKind, setOpenKind] = useState<string | null>(null)
  const ranked = [...groups].sort((a, b) => absMoney(b.end) - absMoney(a.end))
  // Resolved against the data rather than trusted: changing the range or the
  // included accounts can retire a kind the reader had opened.
  const open = ranked.find((group) => group.kind === openKind) ?? null

  const toRegister = (accountId: string) => navigate(registerLinkForAccount(accountId))

  const slices: DonutSlice[] = open
    ? [...open.accounts]
        .sort((a, b) => absMoney(b.end) - absMoney(a.end))
        .map((account, index) => ({
          key: account.account_id,
          label: account.name,
          value: account.end,
          color: seriesColor(index),
          action: `Show ${account.name} in the register`,
        }))
    : ranked.map((group, index) => ({
        key: group.kind,
        label: groupLabel(group),
        value: group.end,
        color: seriesColor(index),
        action:
          group.accounts.length === 0
            ? undefined
            : group.accounts.length === 1
              ? `Show ${group.accounts[0].name} in the register`
              : `Break ${groupLabel(group)} down`,
      }))

  // The same roll-up the panel above uses, so the two totals on one screen are
  // one number rather than two that happen to agree today.
  const total = open ? open.end : treeTotal(groupNodes(groups))

  const select = (slice: DonutSlice) => {
    if (open) {
      toRegister(slice.key)
      return
    }
    const group = ranked.find((one) => one.kind === slice.key)
    if (group === undefined) return
    if (group.accounts.length === 1) toRegister(group.accounts[0].account_id)
    else setOpenKind(group.kind)
  }

  return (
    <>
      <DrillTrail
        label="Breakdown"
        root={caption}
        trail={open === null ? [] : [{ key: open.kind, label: groupLabel(open) }]}
        onStep={() => setOpenKind(null)}
      />

      <p className="breakdown__total">
        <span className="muted">{open ? groupLabel(open) : caption}</span>
        <Money value={total} tone="neutral" />
      </p>

      <DonutGroup onSelect={select}>
        <Donut slices={slices} height={140} signs="absolute" />

        <ul className="legend">
          {slices.map((slice) => (
            <DonutLegendRow
              key={slice.key}
              slice={slice}
              figure={
                asShare ? (
                  <PercentText
                    rate={fractionToPercentUnits(shareOf(slice.value, total))}
                    digits={1}
                    tone="neutral"
                  />
                ) : (
                  <Money value={slice.value} tone="neutral" />
                )
              }
            />
          ))}
        </ul>
      </DonutGroup>
    </>
  )
}

