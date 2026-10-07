import {
  ArrowDownWideNarrow,
  ChartBar,
  ChartPie,
  ChevronDown,
  GitCompareArrows,
  Grid3x3,
  List,
  RotateCcw,
  Shapes,
  Store,
  Tag,
  Workflow,
  X,
} from 'lucide-react'
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { DrillTrail } from '@/components/charts'
import { QueryBoundary } from '@/components/QueryBoundary'
import { FilterPanel } from '@/components/transactions/FilterPanel'
import { useMatchingRows } from '@/components/transactions/matching-rows'
import { MatchingRowsTable } from '@/components/transactions/MatchingRows'
import {
  Button,
  Callout,
  Card,
  ChipGroup,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  IconButton,
  OverflowMenu,
  SearchInput,
  Tooltip,
} from '@/components/ui'
import {
  useSpendingReport,
  type ReportConfig,
  type SavedReport,
  type SpendingCompare,
  type SpendingGrain,
  type SpendingGroup,
  type SpendingReportData,
} from '@/lib/clients/reports'
import { plural } from '@/lib/format'
import {
  GRAINS,
  GROUPS,
  SORTS,
  compareLabel,
  comparisonSentence,
  searchRows,
  sortFlow,
  sortRows,
  type SpendingSort,
} from '@/lib/reports/spending'
import { isDirty, type ReportTab, type TabState } from '@/lib/reports/tabs'
import { readStoredChoice, readStoredFlag, writeStored, writeStoredFlag } from '@/lib/storage'
import { drillFacets, drillLabel, drillUnder, stepFor, type DrillStep } from '@/lib/transactions/drill'
import { toFilterItems, type FilterUniverse } from '@/lib/transactions/filter'
import { registerLinkForWindow } from '@/lib/transactions/links'
import { useAccounts, useAdHocFilter } from '@/lib/transactions/queries'

import { SaveButton } from './SaveButton'
import type { DifferenceUnit } from './spending/amounts'
import { CompareBars } from './spending/CompareBars'
import { PeriodChart } from './spending/PeriodChart'
import { SpendingDonut } from './spending/SpendingDonut'
import { SpendingFlowView } from './spending/SpendingFlowView'
import { HeatLegend, SpendingTableView } from './spending/SpendingTableView'
import { SummaryCards } from './spending/SummaryCards'

type SpendingView = 'bars' | 'donut' | 'flow' | 'table' | 'transactions'

const VIEWS: readonly { value: SpendingView; label: ReactNode; ariaLabel: string; hint: string }[] = [
  { value: 'bars', label: <ChartBar size={14} />, ariaLabel: 'Comparison bars', hint: 'Compare with another period' },
  { value: 'donut', label: <ChartPie size={14} />, ariaLabel: 'Donut', hint: 'Share of spending' },
  { value: 'flow', label: <Workflow size={14} />, ariaLabel: 'Flow', hint: 'Income flowing into spending' },
  { value: 'table', label: <Grid3x3 size={14} />, ariaLabel: 'Table', hint: 'Every period side by side' },
  { value: 'transactions', label: <List size={14} />, ariaLabel: 'Transactions', hint: 'The transactions behind it' },
]

const GROUP_ICONS: Record<SpendingGroup, ReactNode> = {
  tag: <Tag size={13} aria-hidden="true" />,
  payee: <Store size={13} aria-hidden="true" />,
  category: <Shapes size={13} aria-hidden="true" />,
}

const VIEW_KEY = 'reports.spending.view'
const UNIT_KEY = 'reports.spending.difference-unit'
const CENTS_KEY = 'reports.spending.hide-cents'
const RATE_KEY = 'reports.spending.spending-rate'

function asGrain(value: string): SpendingGrain {
  return value === 'quarter' || value === 'year' ? value : 'month'
}

function asGroup(value: string): SpendingGroup {
  return value === 'payee' || value === 'tag' ? value : 'category'
}

/**
 * The Spending report: a month, quarter or year against a comparison, broken
 * down by category, payee or tag (`calculations.md` §11). The grain and the
 * grouping are the tab's config and the account and facet scope its filter,
 * so a saved Spending report keeps all three; the period, the comparison and
 * the view are where the reader is looking and are not saved.
 *
 * Clicking a line drills in as the register's Spending tab does: a category
 * with subcategories opens them beneath a breadcrumb; anything else narrows
 * the report to it and shows its transactions.
 */
export function SpendingReport({
  tab,
  universe,
  header,
  onUpdate,
  onConfig,
  onReset,
  onSaved,
}: {
  tab: ReportTab
  universe: FilterUniverse
  header: (slots: { children?: ReactNode; actions?: ReactNode }) => ReactNode
  onUpdate: (next: Partial<TabState>) => void
  onConfig: (next: Partial<ReportConfig>) => void
  onReset: () => void
  onSaved: (row: SavedReport) => void
}) {
  const grain = asGrain(tab.state.config.time_grain)
  const group = asGroup(tab.state.config.rows)
  const accounts = useAccounts()

  const [period, setPeriod] = useState<string | null>(null)
  const [compare, setCompare] = useState<SpendingCompare>('prior')
  const [drill, setDrill] = useState<DrillStep[]>([])
  const [sort, setSort] = useState<SpendingSort>('largest')
  const [dismissed, setDismissed] = useState(false)
  const [view, setViewState] = useState<SpendingView>(() =>
    readStoredChoice(VIEW_KEY, VIEWS.map((one) => one.value), 'bars'),
  )
  const [unit, setUnitState] = useState<DifferenceUnit>(() =>
    readStoredChoice<DifferenceUnit>(UNIT_KEY, ['pct', 'amount'], 'pct'),
  )
  const [hideCents, setHideCents] = useState(() => readStoredFlag(CENTS_KEY, false))
  const [spendingRate, setSpendingRate] = useState(() => readStoredFlag(RATE_KEY, false))
  const showCents = !hideCents

  const setView = useCallback((next: SpendingView) => {
    setViewState(next)
    writeStored(VIEW_KEY, next)
  }, [])
  const setUnit = (next: DifferenceUnit) => {
    setUnitState(next)
    writeStored(UNIT_KEY, next)
  }

  // The drill narrows the report's own filter, exactly as the register's chart
  // drill narrows the register's: one filter, whichever control narrowed it.
  const under = group === 'category' ? drillUnder(drill) : null
  const items = useMemo(
    () => toFilterItems({ ...tab.state.filter, ...drillFacets(drill) }, universe),
    [tab.state.filter, drill, universe],
  )
  const savedItems = useMemo(() => toFilterItems(tab.state.filter, universe), [tab.state.filter, universe])
  const filter = useAdHocFilter(items, '')
  const report = useSpendingReport(
    { grain, period, compare, groupBy: group, under, filterId: filter.filterId },
    !filter.pending,
  )

  const changeGrain = (next: SpendingGrain) => {
    setPeriod(null)
    setCompare('prior')
    onConfig({ time_grain: next })
  }

  const open = useCallback(
    (line: { key: string; label: string }) => {
      const step = stepFor(group, line, under)
      if (step !== null) setDrill((current) => [...current, step])
      const branches =
        step !== null &&
        group === 'category' &&
        universe.categories.some((category) => category.parent_id === line.key)
      if (!branches) setView('transactions')
    },
    [group, under, universe.categories, setView],
  )

  const crumbs = drill.map((step) => ({ key: `${step.by}:${step.key}`, label: drillLabel(step, universe) }))

  return (
    <>
      {header({
        children: <ChipGroup label="View spend by" value={grain} options={GRAINS} onChange={changeGrain} />,
        actions: (
          <>
            <FilterPanel
              draft={tab.state.filter}
              categories={universe.categories}
              tags={universe.tags}
              accounts={accounts.data ?? []}
              payees={[]}
              onApply={(next) => onUpdate({ filter: next })}
            />
            <OverflowMenu
              label="Options for this report"
              actions={[
                {
                  label: 'Hide cents',
                  checked: hideCents,
                  onCheckedChange: (on) => {
                    setHideCents(on)
                    writeStoredFlag(CENTS_KEY, on)
                  },
                },
              ]}
            />
            {isDirty(tab) ? (
              <Button size="sm" onClick={onReset}>
                <RotateCcw size={13} /> Reset report
              </Button>
            ) : null}
            <SaveButton tab={tab} items={savedItems} queryText="" onSaved={onSaved} />
          </>
        ),
      })}

      <QueryBoundary query={report} rows={8}>
        {(data) => (
          <>
            <PeriodChart data={data} grain={grain} onSelect={setPeriod} />
            <SummaryCards
              data={data}
              showCents={showCents}
              spendingRate={spendingRate}
              onSpendingRate={(on) => {
                setSpendingRate(on)
                writeStoredFlag(RATE_KEY, on)
              }}
            />
            <Breakdown
              data={data}
              grain={grain}
              group={group}
              view={view}
              sort={sort}
              unit={unit}
              compare={compare}
              search={tab.state.search}
              showCents={showCents}
              crumbs={crumbs}
              filterId={filter.filterId}
              filterPending={filter.pending}
              dismissed={dismissed}
              onView={setView}
              onSort={setSort}
              onUnit={setUnit}
              onCompare={setCompare}
              onGroup={(next) => onConfig({ rows: next })}
              onSearch={(next) => onUpdate({ search: next })}
              onStep={(depth) => setDrill((current) => current.slice(0, depth))}
              onDismiss={() => setDismissed(true)}
              onOpen={open}
            />
          </>
        )}
      </QueryBoundary>
    </>
  )
}

/** The breakdown card: its toolbar, the note about rows to categorize, the drill and one view. */
function Breakdown({
  data,
  grain,
  group,
  view,
  sort,
  unit,
  compare,
  search,
  showCents,
  crumbs,
  filterId,
  filterPending,
  dismissed,
  onView,
  onSort,
  onUnit,
  onCompare,
  onGroup,
  onSearch,
  onStep,
  onDismiss,
  onOpen,
}: {
  data: SpendingReportData
  grain: SpendingGrain
  group: SpendingGroup
  view: SpendingView
  sort: SpendingSort
  unit: DifferenceUnit
  compare: SpendingCompare
  search: string
  showCents: boolean
  crumbs: { key: string; label: string }[]
  filterId: string | null
  filterPending: boolean
  dismissed: boolean
  onView: (view: SpendingView) => void
  onSort: (sort: SpendingSort) => void
  onUnit: (unit: DifferenceUnit) => void
  onCompare: (compare: SpendingCompare) => void
  onGroup: (group: SpendingGroup) => void
  onSearch: (search: string) => void
  onStep: (depth: number) => void
  onDismiss: () => void
  onOpen: (line: { key: string; label: string }) => void
}) {
  // A line keeps the colour of its place in the server's order, the most
  // spent first, whatever the view sorts it by.
  const colorOf = useMemo(() => {
    const index = new Map(data.rows.map((row, at) => [row.key, at]))
    return (key: string) => index.get(key) ?? 0
  }, [data.rows])
  const rows = useMemo(() => searchRows(data.rows, search), [data.rows, search])
  const noun = GROUPS.find((one) => one.value === group)?.noun ?? 'lines'
  const matching = useMatchingRows(
    { filterId, from: data.window.from, to: data.window.to },
    view === 'transactions' && !filterPending,
  )
  const uncategorized = data.uncategorized_count

  return (
    <Card className="spend-breakdown">
      <div className="toolbar toolbar--flush toolbar--pack">
        <ChipGroup label="Show the breakdown as" value={view} options={VIEWS} onChange={onView} />
        {view === 'bars' ? <CompareControl data={data} grain={grain} compare={compare} onCompare={onCompare} /> : null}
        {view === 'donut' || view === 'flow' ? <SortMenu sort={sort} onSort={onSort} /> : null}
        {view === 'table' ? <HeatLegend /> : null}
        {(view === 'bars' && data.comparison) || view === 'table' ? (
          <ChipGroup
            label="Show the difference as"
            layout="tight"
            value={unit}
            options={[
              { value: 'pct', label: '%', ariaLabel: 'Percent' },
              { value: 'amount', label: '$', ariaLabel: 'Amount' },
            ]}
            onChange={onUnit}
          />
        ) : null}
        <span className="toolbar__spacer" />
        {view === 'bars' || view === 'table' || view === 'donut' ? (
          <SearchInput
            size="sm"
            value={search}
            placeholder={`Search ${noun}`}
            aria-label={`Search ${noun}`}
            onChange={onSearch}
          />
        ) : null}
        <ChipGroup
          label="Break spending down by"
          value={group}
          options={GROUPS.map((one) => ({
            value: one.value,
            label: (
              <>
                {GROUP_ICONS[one.value]} {one.label}
              </>
            ),
          }))}
          onChange={onGroup}
        />
      </div>

      {uncategorized > 0 && !dismissed ? (
        <Callout
          tone="warning"
          className="spend-breakdown__note callout--wrap"
          actions={
            <>
              <Button size="sm" onClick={onDismiss}>
                Maybe later
              </Button>
              <Button size="sm" variant="primary" asChild>
                <Link to={registerLinkForWindow(data.window, { categoryId: '', tab: 'spending' })}>
                  Categorize now
                </Link>
              </Button>
            </>
          }
        >
          {plural(uncategorized, 'transaction')} {uncategorized === 1 ? 'needs' : 'need'} to be categorized.
        </Callout>
      ) : null}

      <DrillTrail label="Breakdown" root="All spending" trail={crumbs} onStep={onStep} />

      {view === 'bars' ? (
        <CompareBars
          data={data}
          rows={rows}
          grain={grain}
          group={group}
          unit={unit}
          showCents={showCents}
          colorOf={colorOf}
          onOpen={onOpen}
        />
      ) : null}
      {view === 'donut' ? (
        <SpendingDonut rows={sortRows(rows, sort)} showCents={showCents} colorOf={colorOf} onOpen={onOpen} />
      ) : null}
      {view === 'flow' ? (
        <SpendingFlowView
          flow={sortFlow(data.flow, sort)}
          remaining={data.summary.remaining}
          showCents={showCents}
          colorOf={colorOf}
        />
      ) : null}
      {view === 'table' ? (
        <SpendingTableView
          data={data}
          grain={grain}
          group={group}
          search={search}
          unit={unit}
          showCents={showCents}
          onOpen={onOpen}
        />
      ) : null}
      {view === 'transactions' ? (
        <MatchingRowsTable
          matching={matching}
          empty="Nothing in this period."
          showCents={showCents}
        />
      ) : null}
    </Card>
  )
}

/** The Compare chip: what the period is set beside, its dates on hover, and a way to stop. */
function CompareControl({
  data,
  grain,
  compare,
  onCompare,
}: {
  data: SpendingReportData
  grain: SpendingGrain
  compare: SpendingCompare
  onCompare: (compare: SpendingCompare) => void
}) {
  const options = data.compare_options.filter((option) => option !== 'none')
  const trigger = (
    <DropdownMenuTrigger asChild>
      <Button size="sm" variant={compare === 'none' ? 'secondary' : 'primary'}>
        <GitCompareArrows size={13} aria-hidden="true" />
        {compare === 'none' ? 'Compare' : `Compare: ${compareLabel(compare, grain)}`}
        <ChevronDown size={13} aria-hidden="true" />
      </Button>
    </DropdownMenuTrigger>
  )
  return (
    <span className="spend-compare">
      <DropdownMenu>
        {data.comparison ? (
          <Tooltip label={comparisonSentence(data.period, data.comparison)} side="bottom">
            {trigger}
          </Tooltip>
        ) : (
          trigger
        )}
        <DropdownMenuContent align="start">
          <DropdownMenuRadioGroup
            value={compare}
            onValueChange={(next) => onCompare(options.find((one) => one === next) ?? 'none')}
          >
            {options.map((option) => (
              <DropdownMenuRadioItem key={option} value={option}>
                {compareLabel(option, grain)}
              </DropdownMenuRadioItem>
            ))}
            <DropdownMenuSeparator />
            <DropdownMenuRadioItem value="none">{compareLabel('none', grain)}</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {compare === 'none' ? null : (
        <IconButton label="Stop comparing" variant="ghost" size="sm" onClick={() => onCompare('none')}>
          <X size={13} />
        </IconButton>
      )}
      {data.comparison === null && compare !== 'none' ? (
        <span className="toolbar__note">Nothing earlier to compare with</span>
      ) : null}
    </span>
  )
}

/** Largest, smallest, or by name, for the donut and the flow. */
function SortMenu({ sort, onSort }: { sort: SpendingSort; onSort: (sort: SpendingSort) => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm">
          <ArrowDownWideNarrow size={13} aria-hidden="true" />
          {SORTS.find((one) => one.value === sort)?.label}
          <ChevronDown size={13} aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuRadioGroup
          value={sort}
          onValueChange={(next) => onSort(SORTS.find((one) => one.value === next)?.value ?? 'largest')}
        >
          {SORTS.map((one) => (
            <DropdownMenuRadioItem key={one.value} value={one.value}>
              {one.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
