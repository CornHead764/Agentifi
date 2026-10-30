import { useQuery } from '@tanstack/react-query'
import { ArrowLeft, Pencil, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import {
  ACCENT_COLOR,
  AXIS,
  BAR_CURSOR,
  ChartTooltip,
  GRID,
  useMoneyTick,
} from '@/components/charts'
import { Money } from '@/components/Money'
import { useMatchingRows } from '@/components/transactions/matching-rows'
import { MatchingRowsTable } from '@/components/transactions/MatchingRows'
import { LoadFailure } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  IconButton,
  PageHeader,
  SkeletonRows,
  Tabs,
  TabsList,
  TabsTrigger,
  Tooltip as UiTooltip,
  useConfirm,
  useToast,
} from '@/components/ui'
import { moneyToNumber, type Money as MoneyValue } from '@/lib/money'
import { scaled } from '@/lib/scale'
import { formatMonthKey, monthBounds, monthKey, plural } from '@/lib/format'
import {
  WATCHLISTS_KEY,
  averageOfFullMonths,
  averageWindowLabel,
  breakdownFor,
  fullMonths,
  periodLabel,
  sharePercent,
  useCreateWatchlist,
  useDeleteWatchlist,
  useUpdateWatchlist,
  watchlistsApi,
  type BreakdownDimension,
  type MonthSpend,
  type WatchlistSummary,
  watchlistDetailKey,
} from '@/lib/watchlists'

import { WatchlistDialog } from './watchlist/WatchlistDialog'
import { WatchlistDeleteDialog } from './watchlist/WatchlistDeleteDialog'
import { WatchlistTarget, WatchlistTargetBadge } from './watchlist/WatchlistTarget'
import { AskRowButton } from '@/components/assistant/AskMenuItem'
import { watchlistSubject } from '@/lib/assistant/subjects'

/**
 * Watchlists: saved slices of spending, not securities. The trailing average
 * comes from the trend the bars draw, minus the month still running, and the
 * caption names the window.
 */
export function WatchlistPage() {
  // The open watchlist is the URL — /watchlist/<id> — the way Simplifi
  // routes it, so a detail survives reload and back closes it.
  const navigate = useNavigate()
  const openId = useParams().watchlistId ?? null
  const [creating, setCreating] = useState(false)

  const { show } = useToast()

  const watchlists = useQuery({
    queryKey: WATCHLISTS_KEY,
    queryFn: ({ signal }) => watchlistsApi.list(signal),
  })
  const create = useCreateWatchlist()

  if (watchlists.isPending) {
    return (
      <div className="page">
        <SkeletonRows rows={4} />
      </div>
    )
  }

  if (watchlists.isError || !watchlists.data) {
    return (
      <div className="page">
        <LoadFailure query={watchlists} title="Could not load watchlists" />
      </div>
    )
  }

  const open = watchlists.data.find((watchlist) => watchlist.id === openId)
  if (open) {
    return (
      <WatchlistDetail
        watchlist={open}
        onBack={() => navigate('/watchlist')}
      />
    )
  }

  return (
    <div className="page page--wide">
      {watchlists.data.length === 0 ? (
        <EmptyState
          title="No watchlists yet"
          body="Track spending in one category, payee or tag."
          action={
            <Button variant="primary" onClick={() => setCreating(true)}>
              <Plus size={14} /> New watchlist
            </Button>
          }
        />
      ) : (
        <>
          <PageHeader
            actions={
              <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
                <Plus size={13} /> New watchlist
              </Button>
            }
          />
          <ul className="watchlists__grid">
            {watchlists.data.map((watchlist) => (
              <li key={watchlist.id}>
                <Card
                  className="watchlist"
                  title={
                    <>
                      <span aria-hidden="true">{watchlist.emoji ?? '👁'}</span>{' '}
                      <Link className="watchlist__link" to={`/watchlist/${watchlist.id}`}>
                        {watchlist.name}
                      </Link>
                    </>
                  }
                  actions={
                    <>
                      <WatchlistTargetBadge watchlist={watchlist} />
                      <Badge>{periodLabel(watchlist.period)}</Badge>
                    </>
                  }
                >
                  <div className="stack stack--3">
                    <div className="watchlist__stats">
                      <Stat
                        label="12 month"
                        value={averageOfFullMonths(watchlist.monthly_trend)}
                        caption={averageWindowLabel(watchlist.monthly_trend)}
                      />
                      <Stat
                        label="Year to date"
                        value={watchlist.year_to_date}
                        caption={`${new Date().getFullYear()} total`}
                      />
                  </div>

                  <div className="watchlist__stats watchlist__stats--month">
                    <Stat label="This month" value={watchlist.this_month_spent} caption="Spent so far" />
                    <Stat
                      label=""
                      value={watchlist.month_projection}
                      caption="Projected at the current run rate"
                    />
                  </div>

                  <WatchlistTarget watchlist={watchlist} />
                  </div>
                </Card>
              </li>
            ))}
          </ul>
        </>
      )}

      {creating ? (
        <WatchlistDialog
          pending={create.isPending}
          onSubmit={(body) =>
            create.mutateAsync(body).then((created) => {
              show({ title: `Added ${created.name}`, tone: 'success' })
              return created
            })
          }
          onClose={() => setCreating(false)}
        />
      ) : null}
    </div>
  )
}

function Stat({
  label,
  value,
  caption,
}: {
  label: string
  value: MoneyValue
  caption: string
}) {
  return (
    <div className="watchlist__stat">
      {label ? <p className="hint">{label}</p> : null}
      <Money value={value} signs="absolute" tone="neutral" className="watchlist__statvalue figure--stat" />
      <p className="hint hint--faint">{caption}</p>
    </div>
  )
}

function WatchlistDetail({
  watchlist,
  onBack,
}: {
  watchlist: WatchlistSummary
  onBack: () => void
}) {
  const [dimension, setDimension] = useState<BreakdownDimension>('category')
  const [editing, setEditing] = useState(false)
  // The month the breakdown and the transaction list are about, so the bar for
  // a month a person clicks is one they can look inside.
  const [month, setMonth] = useState(() => monthKey())
  const { show } = useToast()
  const remove = useConfirm(useDeleteWatchlist(), {
    variables: (target: WatchlistSummary) => target.id,
    onSuccess: (_result, target) => {
      show({ title: `Deleted ${target.name}`, tone: 'success' })
      onBack()
    },
  })
  const update = useUpdateWatchlist()

  const detail = useQuery({
    queryKey: watchlistDetailKey(watchlist.id, month),
    queryFn: ({ signal }) => watchlistsApi.detail(watchlist.id, month, signal),
  })

  const trend = detail.data?.monthly_trend ?? watchlist.monthly_trend
  const average = averageOfFullMonths(trend)
  const full = fullMonths(trend)

  return (
    <div className="page page--wide">
      <PageHeader
        leading={
          <IconButton label="Back to watchlists" variant="ghost" onClick={onBack}>
            <ArrowLeft size={16} />
          </IconButton>
        }
        title={
          <>
            <span aria-hidden="true">{watchlist.emoji ?? '👁'}</span> {watchlist.name}
          </>
        }
        actions={
          <>
            <AskRowButton subject={() => watchlistSubject(watchlist)} />
            <IconButton
              label={`Edit ${watchlist.name}`}
              variant="ghost"
              size="sm"
              onClick={() => setEditing(true)}
            >
              <Pencil size={14} />
            </IconButton>
            <IconButton
              label={`Delete ${watchlist.name}`}
              variant="ghost"
              size="sm"
              onClick={() => remove.ask(watchlist)}
            >
              <Trash2 size={14} />
            </IconButton>
          </>
        }
      >
        <WatchlistTargetBadge watchlist={watchlist} />
      </PageHeader>

      <Card>
        <div className="stack">
          <div className="watchlist-detail__stats">
            <Stat label="12 month" value={average} caption={averageWindowLabel(trend)} />
            <Stat
              label="Year to date"
              value={watchlist.year_to_date}
              caption={`${new Date().getFullYear()} yearly total`}
            />
            <Stat label="This month" value={watchlist.this_month_spent} caption="Spent so far" />
            <Stat label="" value={watchlist.month_projection} caption="Projected" />
          </div>
          <WatchlistTarget watchlist={watchlist} />
        </div>
      </Card>

      <Card
        title="Overtime"
        actions={
          <UiTooltip
            label={`Average of ${plural(full.length, 'full month')}. The lighter current month is not in it.`}
            side="left"
          >
            <span className="watchlist-detail__legend">
              Monthly average <Money value={average} signs="absolute" tone="neutral" />
            </span>
          </UiTooltip>
        }
      >
        <TrendChart trend={trend} selected={month} onSelect={setMonth} />
      </Card>

      {detail.data ? (
        <Card
          title="Breakdown"
          subtitle={formatMonthKey(detail.data.month)}
          actions={
            <>
              {month === monthKey() ? null : (
                <Button size="sm" onClick={() => setMonth(monthKey())}>
                  Back to this month
                </Button>
              )}
              {/* The three splits are computed in the same pass and all sent, so
                  switching between them costs no request. */}
              <Tabs
                value={dimension}
                onValueChange={(value) => setDimension(asDimension(value))}
              >
                <TabsList>
                  <TabsTrigger value="category">Category</TabsTrigger>
                  <TabsTrigger value="payee">Payee</TabsTrigger>
                  <TabsTrigger value="tag">Tag</TabsTrigger>
                </TabsList>
              </Tabs>
            </>
          }
        >
          <ul className="watchlist-detail__breakdown">
            {breakdownFor(detail.data, dimension).map((row) => (
              <li key={row.key || 'unassigned'} className="breakdown-chip">
                <span className="breakdown-chip__share">{sharePercent(row)}</span>
                <span>
                  <strong>{row.label || 'Unassigned'}</strong>
                  <Money value={row.spent} signs="absolute" tone="neutral" />
                </span>
              </li>
            ))}
          </ul>
        </Card>
      ) : null}

      {detail.data ? (
        <WatchlistTransactions filterId={detail.data.filter_id} month={detail.data.month} />
      ) : null}

      {editing ? (
        <WatchlistDialog
          editing={watchlist}
          pending={update.isPending}
          onSubmit={(body) => update.mutateAsync({ id: watchlist.id, body })}
          onClose={() => setEditing(false)}
        />
      ) : null}

      <WatchlistDeleteDialog confirm={remove} />
    </div>
  )
}

/** Tabs hand back a bare string; the page opens on the category split. */
function asDimension(value: string): BreakdownDimension {
  return value === 'payee' || value === 'tag' ? value : 'category'
}

/**
 * The Overtime bars, with the month in progress drawn lighter: shown, but no
 * average may use it, and a short final bar must not read as a spending drop.
 */
function TrendChart({
  trend,
  selected,
  onSelect,
}: {
  trend: MonthSpend[]
  selected: string
  onSelect: (month: string) => void
}) {
  const tick = useMoneyTick()
  const data = trend.map((month) => ({
    month: month.month,
    label: formatMonthKey(month.month, 'month'),
    plot: moneyToNumber(month.spent),
    spent: month.spent,
    partial: month.is_partial,
  }))

  return (
    <div className="watchlist-detail__plot">
      <ResponsiveContainer width="100%" height={scaled(220)}>
        <BarChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid {...GRID} vertical={false} />
          <XAxis dataKey="label" {...AXIS} />
          <YAxis tickFormatter={tick} {...AXIS} width={scaled(56)} />
          <Tooltip
            cursor={BAR_CURSOR}
            content={({ label }) => {
              const point = data.find((entry) => entry.label === label)
              if (!point) return null
              return (
                <ChartTooltip
                  title={point.partial ? `${point.label} (so far)` : point.label}
                  rows={[{ label: 'Spent', value: point.spent, color: ACCENT_COLOR, signs: 'absolute' }]}
                />
              )
            }}
          />
          <Bar
            dataKey="plot"
            radius={[3, 3, 0, 0]}
            isAnimationActive={false}
            className="watchlist-detail__bar"
            onClick={(_: unknown, index: number) => onSelect(data[index]?.month ?? selected)}
          >
            {data.map((row) => (
              <Cell
                key={row.label}
                fill={ACCENT_COLOR}
                fillOpacity={row.partial ? 0.45 : 1}
                stroke={row.month === selected ? 'var(--text)' : undefined}
                strokeWidth={row.month === selected ? 1.5 : undefined}
              />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  )
}

/** The rows behind the figures, for the selected month. */
function WatchlistTransactions({ filterId, month }: { filterId: string; month: string }) {
  const matching = useMatchingRows({ filterId, ...monthBounds(month) })

  return (
    <Card
      title="Transactions"
      subtitle={formatMonthKey(month)}
      flush
      actions={
        matching.splitCount > 0 ? (
          <UiTooltip
            label="Split rows count only their matching parts above; full amounts show here."
            side="left"
          >
            <span className="watchlist-detail__legend">
              {plural(matching.splitCount, 'split row')}
            </span>
          </UiTooltip>
        ) : null
      }
    >
      <MatchingRowsTable matching={matching} empty="Nothing matched this month." />
    </Card>
  )
}
