import {
  ArrowLeftRight,
  Eye,
  Landmark,
  Pencil,
  PiggyBank,
  RefreshCw,
} from 'lucide-react'
import { useRef, useState } from 'react'
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip as RechartsTooltip,
  XAxis,
  YAxis,
} from 'recharts'

import {
  AXIS,
  ChartTooltip as ChartTooltipWrapper,
  GRID,
  LINE_CURSOR,
  useMoneyTick,
} from '@/components/charts'
import { Money, MoneyTotals } from '@/components/Money'
import {
  Badge,
  Card,
  ContextMenu,
  ContextMenuCheckboxItem,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuTrigger,
  OverflowMenuButton,
  Tooltip,
  openContextMenuAt,
} from '@/components/ui'
import { maskedNumber } from '@/lib/accounts'
import { useUpdateAccountSettings } from '@/lib/clients/connections'
import { useBalanceAdjustments } from '@/lib/transactions/queries'
import { AccountDetailsDialog } from '@/pages/settings/AccountDetailsDialog'
import { useRepriceAccount } from '@/pages/settings/useRepriceAccount'

import { TransferMoneyDialog } from './TransferMoneyDialog'
import { moneyToNumber, subMoney, sumByCurrency, type Money as MoneyValue } from '@/lib/money'
import type { AccountWithBalances, Transaction } from '@/lib/transactions/types'
import { scaled } from '@/lib/scale'
import { accountFreshnessBadge } from './accountFreshness'
import { valuationSourceLabel } from '@/lib/accountTypes'
import { AskContextMenuItem } from '@/components/assistant/AskMenuItem'
import { accountSubject } from '@/lib/assistant/subjects'
import { formatDate, formatPercent, parseRate, timeAgo } from '@/lib/format'

/**
 * The context header above the register. Balances come from `/accounts`,
 * which computes all four from one posting list, so they cannot disagree.
 */
export function AllAccountsHeader({
  accounts,
  title,
}: {
  accounts: readonly AccountWithBalances[]
  /** The drawer node these accounts are, when it is narrower than all of them. */
  title?: string | null
}) {
  const included = accounts.filter((account) => !account.is_closed)
  const total = sumByCurrency(
    included.map((account) => ({ currency: account.currency, amount: account.balances.balance })),
  )
  const goals = sumByCurrency(
    included.map((account) => ({ currency: account.currency, amount: account.goal_balance })),
  )
  const available = sumByCurrency(
    included.map((account) => ({
      currency: account.currency,
      amount: subMoney(account.balances.balance, account.goal_balance),
    })),
  )

    // With no goals reserved, Available is the balance already printed, so the
    // goals line is shown only when something is reserved.
  const reserved = goals.some((one) => one.amount !== 0)

  return (
    <Card flush>
      <div className="context-card context-card--line">
        <div className="context-card__identity">
          <span className="context-card__logo">
            <Landmark size={16} />
          </span>
          <p className="context-card__title">
            {title ?? 'All Accounts'}{' '}
            <span className="hint">· {included.length} accounts</span>
          </p>
        </div>
        <div className="context-card__figures">
          <p className="figure--total">
            <MoneyTotals totals={total} tone="neutral" />
          </p>
          {reserved ? (
            <p className="hint">
              Savings goals <MoneyTotals totals={goals} tone="neutral" /> · Available{' '}
              <MoneyTotals totals={available} tone="neutral" />
            </p>
          ) : null}
        </div>
      </div>
    </Card>
  )
}


export function AccountHeader({
  account,
  accounts,
}: {
  account: AccountWithBalances
  /** Every account, for the transfer dialog's other half. */
  accounts: readonly AccountWithBalances[]
}) {
  const freshness = accountFreshnessBadge(account)
  const update = useUpdateAccountSettings()
  const reprice = useRepriceAccount(account)
  const [editing, setEditing] = useState(false)
  const [transferring, setTransferring] = useState(false)
  const menuButton = useRef<HTMLButtonElement>(null)

  return (
    <Card flush>
      {/* A context menu over the whole header, not a third flex child, which
          would push the balance off the edge. */}
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div>
            <div className="context-card">
              <div className="context-card__identity">
                <span className="context-card__logo">
                  {account.logo_url ? (
                    <img src={account.logo_url} alt="" />
                  ) : (
                    <Landmark size={16} />
                  )}
                </span>
                <div>
                  <p className="context-card__title">{account.name}</p>
                  <p className="context-card__meta">
                    {account.masked_number ? <span>{maskedNumber(account.masked_number)}</span> : null}
                    <Badge tone={freshness.tone}>{freshness.label}</Badge>
                  </p>
                </div>
              </div>
              <div className="context-card__figures">
                <p className="figure--total">
                  <Money value={account.balances.balance} tone="neutral" />
                </p>
                <p className="hint">
                  Balance w/ pending{' '}
                  <Money value={account.balances.balance_with_pending} tone="neutral" />
                </p>
              </div>

              {/* The same menu right-click and long-press open, and the
                  element the platform's context-menu key fires on. */}
              <OverflowMenuButton
                ref={menuButton}
                className="context-card__menu"
                label={`Actions for ${account.name}`}
                onClick={() => openContextMenuAt(menuButton.current)}
              />
            </div>

            {account.kind === 'credit_card' ? <CreditStrip account={account} /> : null}
            {account.kind === 'asset' ? (
              <AssetStrip account={account} />
            ) : null}
          </div>
        </ContextMenuTrigger>

        {/* Simplifi's *Disable account transfers* is absent: no column
            backs it. */}
        <ContextMenuContent>
          <ContextMenuItem icon={<Pencil size={14} />} onSelect={() => setEditing(true)}>
            Edit account
          </ContextMenuItem>
          <ContextMenuItem
            icon={<ArrowLeftRight size={14} />}
            onSelect={() => setTransferring(true)}
          >
            Transfer money…
          </ContextMenuItem>
          {reprice.available ? (
            <ContextMenuItem
              icon={<RefreshCw size={14} />}
              disabled={reprice.pending}
              onSelect={reprice.reprice}
            >
              {reprice.pending ? 'Re-pricing…' : 'Re-price value'}
            </ContextMenuItem>
          ) : null}
          <AskContextMenuItem
            subject={() =>
              accountSubject({
                id: account.id,
                name: account.name,
                balance: account.balances.balance,
                kind: account.kind,
              })
            }
          />

          <ContextMenuSeparator />
          <ContextMenuLabel>Exclude account transactions from</ContextMenuLabel>
          <ContextMenuCheckboxItem
            checked={account.excluded_from_spending_plan}
            onCheckedChange={(checked) =>
              update.mutate({
                id: account.id,
                patch: { excluded_from_spending_plan: checked === true },
              })
            }
          >
            <PiggyBank size={14} /> Spending Plan
          </ContextMenuCheckboxItem>
          <ContextMenuCheckboxItem
            checked={account.excluded_from_reports}
            onCheckedChange={(checked) =>
              update.mutate({
                id: account.id,
                patch: { excluded_from_reports: checked === true },
              })
            }
          >
            <Eye size={14} /> Reports
          </ContextMenuCheckboxItem>
        </ContextMenuContent>
      </ContextMenu>

      <AccountDetailsDialog account={account} open={editing} onOpenChange={setEditing} />
      {transferring ? (
        <TransferMoneyDialog
          open
          onOpenChange={setTransferring}
          from={account}
          accounts={accounts}
          onDone={() => setTransferring(false)}
        />
      ) : null}
    </Card>
  )
}

/**
 * A credit card's statement figures, only the ones that exist: any can be
 * absent, and an absent figure is left out rather than drawn as a dash.
 * `credit_used_pct` null ("no limit on file") hides the gauge, which zero
 * ("none used") does not.
 */
function CreditStrip({ account }: { account: AccountWithBalances }) {
  const used = account.balances.credit_used_pct
  const rate = account.interest_rate
  if (
    account.statement_balance === null &&
    account.minimum_due === null &&
    account.due_date === null &&
    rate === null &&
    used === null
  ) {
    return null
  }

  // Whichever of the last two survives takes the right edge, so the strip does
  // not end in the middle of the card when the issuer reports no rate.
  const rightward = rate === null ? 'credit' : 'rate'

  return (
    <div className="strip">
      {account.statement_balance === null ? null : (
        <Figure label="Statement balance" value={account.statement_balance} />
      )}
      {account.minimum_due === null ? null : (
        <Figure label="Minimum due" value={account.minimum_due} />
      )}
      {account.due_date === null ? null : (
        <div className="strip__item">
          <p className="strip__label">Due date</p>
          <p className="strip__value">{formatDate(account.due_date)}</p>
        </div>
      )}
      {rate === null ? null : (
        <div className={rightward === 'rate' ? 'strip__item strip--right' : 'strip__item'}>
          <p className="strip__label">
            Interest rate
            <Tooltip
              label="The rate the issuer reports, or the one you entered. Never computed."
              side="top"
            >
              <span aria-hidden="true">ⓘ</span>
            </Tooltip>
          </p>
          <p className="strip__value">{formatPercent(parseRate(rate))}</p>
        </div>
      )}
      {used === null ? null : (
        <>
          <div className={rightward === 'credit' ? 'strip__item strip--right' : 'strip__item'}>
            <p className="strip__label">Credit used</p>
            <p className="strip__value">{formatPercent(parseRate(used), { digits: 0 })}</p>
          </div>
          <Gauge fraction={Number(used)} />
        </>
      )}
    </div>
  )
}

function AssetStrip({ account }: { account: AccountWithBalances }) {
  const adjustments = useBalanceAdjustments(account.id)
  const series = valuationSeries(account.balances.balance, adjustments.data ?? [])
  const moneyTick = useMoneyTick()

  return (
    <>
      <div className="strip">
        <div className="strip__item">
          <p className="strip__label">Valuation</p>
          <p className="strip__value">{valuationLine(account)}</p>
        </div>
        <div className="strip__item">
          <p className="strip__label">Value as of today</p>
          <p className="strip__value">
            <Money value={account.balances.balance} tone="neutral" />
          </p>
        </div>
      </div>
      {series.length > 1 ? (
        <div className="strip strip--chart">
          <p className="strip__label">Market value</p>
          <ResponsiveContainer width="100%" height={scaled(140)}>
            <AreaChart data={series}>
              <CartesianGrid {...GRID} vertical={false} />
              <XAxis dataKey="label" {...AXIS} minTickGap={32} />
              {/* Masked tick and tooltip: a bare axis or default tooltip would
                  print exact valuations with privacy mode on. */}
              <YAxis width={scaled(64)} {...AXIS} tickFormatter={moneyTick} />
              <RechartsTooltip cursor={LINE_CURSOR} content={ValuationTooltip} />
              <Area
                dataKey="value"
                name="Value"
                stroke="var(--series-1)"
                fill="var(--series-1)"
                fillOpacity={0.25}
              />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      ) : null}
    </>
  )
}

/**
 * An asset's value over time: the server has no valuation-history read, so
 * today's balance minus each adjustment, newest first, is the series.
 */
function valuationSeries(
  balance: MoneyValue,
  adjustments: readonly Transaction[],
): { label: string; value: number; amount: MoneyValue }[] {
  const ordered = [...adjustments].sort((a, b) => a.date.localeCompare(b.date))
  let running = balance
  const points: { label: string; value: number; amount: MoneyValue }[] = []
  for (let index = ordered.length - 1; index >= 0; index -= 1) {
    points.unshift({
      label: formatDate(ordered[index].date, 'month'),
      value: moneyToNumber(running),
      amount: running,
    })
    running = subMoney(running, ordered[index].amount)
  }
  points.push({ label: 'Today', value: moneyToNumber(balance), amount: balance })
  return points
}


/** "Zillow · Updated N days ago", from the same fields the freshness badge reads. */
function valuationLine(account: AccountWithBalances): string {
  if (account.valuation_source === null) return 'Manual'
  const source = valuationSourceLabel(account.valuation_source)
  if (account.valued_at === null) return source
  return `${source} · Updated ${timeAgo(account.valued_at, 'long')}`
}

function isValuationPoint(value: unknown): value is { label: string; amount: MoneyValue } {
  return (
    typeof value === 'object' && value !== null &&
    'label' in value && typeof value.label === 'string' &&
    'amount' in value && typeof value.amount === 'number'
  )
}

/** The market-value chart's hover card; recharts hands it the hovered point untyped. */
export function ValuationTooltip({ payload }: { payload?: readonly { payload?: unknown }[] }) {
  const raw = payload?.[0]?.payload
  if (raw === undefined || !isValuationPoint(raw)) return null
  return (
    <ChartTooltipWrapper title={raw.label} rows={[{ label: 'Market value', value: raw.amount }]} />
  )
}

/** Only ever drawn for a figure that exists; the caller decides that. */
function Figure({ label, value }: { label: string; value: MoneyValue }) {
  return (
    <div className="strip__item">
      <p className="strip__label">{label}</p>
      <p className="strip__value">
        <Money value={value} tone="neutral" />
      </p>
    </div>
  )
}


function Gauge({ fraction }: { fraction: number }) {
  const clamped = Math.min(Math.max(fraction, 0), 1)
  const radius = 16
  const circumference = Math.PI * radius
  return (
    <svg className="gauge" width="44" height="30" viewBox="0 0 44 30" aria-hidden="true">
      <path
        d="M 6 26 A 16 16 0 0 1 38 26"
        fill="none"
        stroke="var(--surface-3)"
        strokeWidth="5"
        strokeLinecap="round"
      />
      <path
        d="M 6 26 A 16 16 0 0 1 38 26"
        fill="none"
        stroke={clamped > 0.75 ? 'var(--warning)' : 'var(--accent)'}
        strokeWidth="5"
        strokeLinecap="round"
        strokeDasharray={`${circumference * clamped} ${circumference}`}
      />
    </svg>
  )
}
