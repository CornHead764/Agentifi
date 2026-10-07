import {
  ArrowDownToLine,
  ArrowUpFromLine,
  Archive,
  ArchiveRestore,
  ChevronDown,
  ChevronRight,
  History,
  Pencil,
  Plus,
  Receipt,
  Search,
  Trash2,
  Wallet,
} from 'lucide-react'
import { useMemo, useState } from 'react'

import { Facts } from '@/components/Facts'
import { GoalEditor } from '@/components/GoalEditor'
import { GoalTransactionDialog, type GoalDialogMode } from '@/components/GoalTransactionDialog'
import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { LoadFailure } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Card,
  ConfirmDialog,
  EmptyState,
  Meter,
  OverflowMenu,
  PageHeader,
  RowActions,
  SkeletonRows,
  Tabs,
  TabsList,
  TabsTrigger,
  Tooltip,
  useConfirm,
  useToast,
} from '@/components/ui'
import {
  goalBarSegments,
  isOpenGoal,
  reservedInAccount,
  useCloseGoal,
  useDeleteGoal,
  type Goal,
  type GoalStage,
  useGoals,
} from '@/lib/goals'
import { ZERO_MONEY, addMoney, moneyToNumber, type Money as MoneyValue } from '@/lib/money'
import { useAccounts } from '@/lib/transactions/queries'
import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { goalSubject } from '@/lib/assistant/subjects'
import { formatDate, formatPercentUnits, plural } from '@/lib/format'

/**
 * Savings goals, by goal or by the account that holds them. A goal's saved
 * figure inflates its account's `goal_balance`, and the *available* balance
 * subtracts it: one pot of money seen twice, which the by-account grouping
 * makes visible.
 */
export function GoalsPage() {
  const [grouping, setGrouping] = useState<'goal' | 'account'>('goal')
  const goals = useGoals()
  const accounts = useAccounts()

  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Goal | null>(null)
  const [moving, setMoving] = useState<{ goal: Goal; mode: GoalDialogMode } | null>(null)

  const { show } = useToast()
  const remove = useConfirm(useDeleteGoal(), {
    variables: (goal: Goal) => goal.id,
    onSuccess: (_result, goal) => show({ title: `Deleted ${goal.name}`, tone: 'success' }),
  })
  const closing = useCloseGoal()
  const moneyText = useMoneyText()
  const setClosed = (goal: Goal, close: boolean) =>
    closing.mutate(
      { id: goal.id, close },
      {
        onSuccess: (updated) =>
          show(
            close
              ? {
                  title: `Closed ${updated.name}`,
                  description: `${moneyText(updated.saved_so_far)} in ${updated.account_name} is available again.`,
                }
              : {
                  title: `Reopened ${updated.name}`,
                  description: `${moneyText(updated.saved_so_far)} is set aside in ${updated.account_name} again.`,
                },
          ),
      },
    )
  const [showClosed, setShowClosed] = useState(false)

  // A goal reserves inside an account's *available* balance, which only a
  // cash account carries.
  const cashAccounts = useMemo(
    () => (accounts.data ?? []).filter((account) => account.kind === 'cash'),
    [accounts.data],
  )

  if (goals.isPending) {
    return (
      <div className="page">
        <SkeletonRows rows={5} />
      </div>
    )
  }

  if (goals.isError || !goals.data) {
    return (
      <div className="page">
        <LoadFailure query={goals} title="Could not load savings goals" />
      </div>
    )
  }

  const active = goals.data.filter(isOpenGoal)
  const closed = goals.data.filter((goal) => !isOpenGoal(goal))

  const cards = (list: Goal[]) => (
    <ul className="goals__grid">
      {list.map((goal) => (
        <li key={goal.id}>
          <GoalCard
            goal={goal}
            onEdit={() => setEditing(goal)}
            onDelete={() => remove.ask(goal)}
            onClose={(close) => setClosed(goal, close)}
            onMove={(mode) => setMoving({ goal, mode })}
          />
        </li>
      ))}
    </ul>
  )

  return (
    <div className="page page--wide">
      <Tabs value={grouping} onValueChange={(value) => setGrouping(value === 'account' ? 'account' : 'goal')}>
        <PageHeader
          tabs={
            <TabsList>
              <TabsTrigger value="goal">By goal</TabsTrigger>
              <TabsTrigger value="account">By account</TabsTrigger>
            </TabsList>
          }
          actions={
            <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
              <Plus size={13} /> New goal
            </Button>
          }
        />
      </Tabs>

      {active.length === 0 ? (
        <EmptyState
          title="No active goals"
          body="Money set aside for a goal stops counting toward its account's available balance."
        />
      ) : grouping === 'goal' ? (
        cards(active)
      ) : (
        byAccount(active).map(([accountName, group]) => (
          <section key={accountName} className="stack stack--3">
            <header className="goals__accounthead">
              <h2>
                <Wallet size={14} aria-hidden="true" /> {accountName}
              </h2>
              <p>
                Reserved here:{' '}
                <Money value={reservedInAccount(group[0].account_id, group)} tone="neutral" /> — this
                much is subtracted from the account&apos;s available balance.
              </p>
            </header>
            {cards(group)}
          </section>
        ))
      )}

      {closed.length === 0 ? null : (
        <section className="stack stack--3">
          <h2>
            <button
              type="button"
              className="goals__closedtoggle"
              aria-expanded={showClosed}
              onClick={() => setShowClosed((shown) => !shown)}
            >
              {showClosed ? <ChevronDown size={14} /> : <ChevronRight size={14} />} Closed (
              {closed.length})
            </button>
          </h2>
          {showClosed ? cards(closed) : null}
        </section>
      )}

      <GoalEditor open={creating} onOpenChange={setCreating} accounts={cashAccounts} />
      <GoalEditor
        open={editing !== null}
        onOpenChange={(open) => {
          if (!open) setEditing(null)
        }}
        goal={editing}
        accounts={cashAccounts}
      />

      <GoalTransactionDialog
        // The card's goal goes stale the moment a row is counted; the list
        // is the fresh copy, so the dialog reads the goal back out of it.
        goal={moving ? (goals.data?.find((one) => one.id === moving.goal.id) ?? null) : null}
        mode={moving?.mode ?? null}
        goals={goals.data ?? []}
        accounts={accounts.data ?? []}
        onMode={(mode) => setMoving((open) => (open ? { ...open, mode } : open))}
        onClose={() => setMoving(null)}
      />

      {/* The server soft-deletes the goal, so its transactions keep their link;
          the account stops subtracting the reserve. */}
      <ConfirmDialog
        {...remove.dialog}
        title={remove.target ? `Delete ${remove.target.name}?` : 'Delete this goal?'}
        confirmLabel="Delete goal"
      >
        {remove.target ? (
          <>
            <p>
              <Money value={remove.target.saved_so_far} tone="neutral" /> reserved in{' '}
              <strong>{remove.target.account_name}</strong> becomes spendable again.
            </p>
            <p className="muted">
              Transactions keep their link to the goal. No charge or spending history changes.
            </p>
          </>
        ) : null}
      </ConfirmDialog>
    </div>
  )
}

const STAGES: Record<GoalStage, { label: string; tone: 'neutral' | 'accent' | 'income'; hint: string }> = {
  saving: { label: 'Saving', tone: 'neutral', hint: 'Still saving toward the target.' },
  funded: {
    label: 'Funded',
    tone: 'income',
    hint: 'The target was reached and the money is still set aside.',
  },
  spending: {
    label: 'Spending',
    tone: 'accent',
    hint: 'Money has been taken out to spend on it. Link the purchases to see where it went.',
  },
  closed: {
    label: 'Closed',
    tone: 'neutral',
    hint: 'Nothing is set aside any more. Its history stays here; reopen it to set the money aside again.',
  },
}

export function GoalCard({
  goal,
  onEdit,
  onDelete,
  onClose,
  onMove,
}: {
  goal: Goal
  onEdit: () => void
  onDelete: () => void
  onClose: (close: boolean) => void
  onMove: (mode: GoalDialogMode) => void
}) {
  // The server's own figures from internal/domain; a client recomputation
  // could disagree across a month boundary. A goal saved for and then spent
  // reaches the end.
  const { saved, spent, reached } = goalBarSegments(goal)
  const stage = STAGES[goal.stage]
  const closed = goal.stage === 'closed'
  const saving = goal.stage === 'saving'

  return (
    <Card
      title={
        <>
          <span className="goal__emoji" aria-hidden="true">
            {goal.emoji ?? '🎯'}
          </span>{' '}
          {goal.name}
        </>
      }
      actions={
        <>
          <Tooltip label={stage.hint} side="top">
            <Badge tone={stage.tone}>{stage.label}</Badge>
          </Tooltip>
          {closed ? null : goal.is_taken_from_plan ? (
            <Tooltip
              label="Contributions appear in the spending plan's Goals bucket."
              side="top"
            >
              <Badge tone="accent">From the plan</Badge>
            </Tooltip>
          ) : (
            <Tooltip
              label="Funded from money the plan already counted, so not charged twice."
              side="top"
            >
              <Badge>Outside the plan</Badge>
            </Tooltip>
          )}
          <RowActions>
            <OverflowMenu
              label={`Actions for ${goal.name}`}
              actions={[
                {
                  label: 'Contribute…',
                  icon: <ArrowDownToLine size={14} />,
                  onSelect: () => onMove('contribute'),
                },
                {
                  label: 'Withdraw…',
                  icon: <ArrowUpFromLine size={14} />,
                  onSelect: () => onMove('withdraw'),
                },
                { label: 'Record spending…', icon: <Receipt size={14} />, onSelect: () => onMove('spend') },
                {
                  label: 'Find rows…',
                  icon: <Search size={14} />,
                  onSelect: () => onMove(saving ? 'find:contribute' : 'find:spend'),
                },
                <AskMenuItem subject={() => goalSubject(goal)} />,
                {
                  label: 'Goal details…',
                  icon: <History size={14} />,
                  onSelect: () => onMove('history'),
                },
                closed
                  ? {
                      label: 'Reopen goal',
                      icon: <ArchiveRestore size={14} />,
                      onSelect: () => onClose(false),
                    }
                  : { label: 'Close goal', icon: <Archive size={14} />, onSelect: () => onClose(true) },
                { label: 'Delete goal', icon: <Trash2 size={14} />, danger: true, onSelect: onDelete },
              ]}
              sections={[{ entries: [{ label: 'Edit goal…', icon: <Pencil size={14} />, onSelect: onEdit }] }]}
            />
          </RowActions>
        </>
      }
    >
      <div className="stack stack--3">
        <Meter
          size="lg"
          label={`Progress toward ${goal.name}`}
          value={reached ?? 0}
          segments={[
            { value: saved, tone: 'series' },
            { value: spent, tone: 'series', faded: true },
          ]}
          reading={
            reached === null ? undefined : { text: formatPercentUnits(reached, { digits: 0 }), at: reached }
          }
        />

        {saving ? <SavingFigures goal={goal} /> : <GoalFlow goal={goal} onFind={() => onMove('find:spend')} />}

        <GoalBreakdown goal={goal} onOpen={() => onMove('history')} />

        <footer className="hint">
          <Facts
            facts={[
              {
                label: closed ? 'Was reserved in' : 'Reserved in',
                value:
                  goal.funding.length > 1
                    ? `${goal.account_name}, funded from ${goal.funding.length} accounts`
                    : goal.account_name,
              },
              saving ? perMonthFact(goal) : null,
              !saving || goal.contributed_this_month === 0
                ? null
                : {
                    label: 'Contributed this month',
                    value: <Money value={goal.contributed_this_month} showPlus />,
                  },
              goal.completed_on ? { label: 'Completed', value: formatDate(goal.completed_on) } : null,
              goal.closed_on ? { label: 'Closed', value: formatDate(goal.closed_on) } : null,
            ]}
          />
          <p>
            {goal.contributions.length === 0 ? (
              <>
                Nothing counted yet.{' '}
                <button type="button" className="goal__link" onClick={() => onMove('find:contribute')}>
                  Find rows
                </button>
              </>
            ) : (
              <button type="button" className="goal__link" onClick={() => onMove('history')}>
                {plural(goal.contributions.length, 'transaction')} counted
              </button>
            )}
          </p>
        </footer>
      </div>
    </Card>
  )
}

/** Saving toward the target: what is in, what is left and the goal. */
function SavingFigures({ goal }: { goal: Goal }) {
  return (
    <dl className="goal__figures">
      <div>
        <dt>Saved</dt>
        <dd>
          <Money value={goal.saved_so_far} tone="neutral" />
        </dd>
      </div>
      <div>
        <dt>Left to save</dt>
        <dd>
          <Money value={goal.left_to_save} tone="neutral" />
        </dd>
      </div>
      <div>
        <dt>Goal</dt>
        <dd>
          <Money value={goal.target_amount} tone="neutral" />
        </dd>
      </div>
    </dl>
  )
}

function perMonthFact(goal: Goal) {
  const needed = goal.monthly_needed
  const months = goal.months_to_target
  if (needed === null) return { label: 'Per month', value: 'No target date' }
  // The server clamps a past date to one month left, which is the right
  // arithmetic and the wrong row: it would ask for the whole remainder "over
  // the next 1 month" of a date long gone.
  if (goal.target_has_passed) {
    return {
      label: `Target ${goal.target_on ? formatDate(goal.target_on) : ''} passed`,
      value: (
        <>
          <Money value={goal.left_to_save} tone="neutral" /> to save
        </>
      ),
    }
  }
  return {
    label: `Per month, ${months} ${months === 1 ? 'month' : 'months'} to ${
      goal.target_on ? formatDate(goal.target_on) : ''
    }`,
    value: <Money value={needed} tone="neutral" />,
  }
}

/**
 * Once funded, where the money went rather than what is left to save: saved in,
 * taken out, spent, what was taken out and not yet linked to a purchase, and
 * what is still in the account.
 */
function GoalFlow({ goal, onFind }: { goal: Goal; onFind: () => void }) {
  const closed = goal.stage === 'closed'
  return (
    <>
      <ol className="goal-flow" aria-label={`Where the money for ${goal.name} went`}>
        <li>
          <span>Saved</span>
          <Money value={goal.funded} tone="neutral" />
        </li>
        <li>
          <span>Taken out</span>
          <Money value={goal.withdrawn} signs="absolute" tone="neutral" />
        </li>
        <li>
          <span>Spent</span>
          <Money value={goal.spent_on_goal} signs="absolute" tone="neutral" />
        </li>
      </ol>
      <dl className="goal__figures">
        {goal.unassigned_withdrawn === 0 ? null : (
          <div>
            <dt>Not yet linked to spending</dt>
            <dd>
              <Money value={goal.unassigned_withdrawn} tone="neutral" />{' '}
              <button type="button" className="goal__link" onClick={onFind}>
                Find rows
              </button>
            </dd>
          </div>
        )}
        <div>
          <dt>{closed ? `Left in ${goal.account_name}` : `Still in ${goal.account_name}`}</dt>
          <dd>
            <Money value={goal.saved_so_far} tone="neutral" />
          </dd>
        </div>
        <div>
          <dt>Goal</dt>
          <dd>
            <Money value={goal.target_amount} tone="neutral" />
          </dd>
        </div>
      </dl>
      {goal.stage === 'funded' ? (
        <p className="hint">
          Saved in full. When you move it out to spend, count that transfer as taken out, then link
          the purchases.
        </p>
      ) : null}
    </>
  )
}

/** How many categories the card lists before rolling the tail into one row. */
const BREAKDOWN_ROWS = 6

/**
 * What a goal's money went on, by category: a goal is a second axis across the
 * category tree, so a charge keeps its ordinary category.
 *
 * The tail is rolled into one row rather than truncated, so the parts add up
 * to the total above. Bars are drawn against the largest row, not the total,
 * since the point is comparing rows.
 */
function GoalBreakdown({ goal, onOpen }: { goal: Goal; onOpen: () => void }) {
  const lines = goal.spending_by_category
  if (lines.length === 0) return null

  const shown = lines.slice(0, BREAKDOWN_ROWS)
  const rest = lines.slice(BREAKDOWN_ROWS)
  const widest = Math.max(...lines.map((one) => Math.abs(moneyToNumber(one.spent))), 1)
  const restTotal = rest.reduce((total, one) => addMoney(total, one.spent), ZERO_MONEY)

  const row = (key: string, label: string, amount: MoneyValue, count: number) => (
    <li key={key}>
      <span className="goal-split__label" title={label}>
        {label}
      </span>
      <Meter
        size="sm"
        className="goal-split__bar"
        segments={[{ value: (Math.abs(moneyToNumber(amount)) / widest) * 100 }]}
      />
      <span className="goal-split__amount">
        <Money value={amount} signs="absolute" tone="neutral" />
        <small>
          {plural(count, 'row')}
        </small>
      </span>
    </li>
  )

  // The total lives in this heading; the breakdown under it is what it went
  // on.
  const spending = goal.contributions.filter((row) => row.kind === 'spending').length

  return (
    <section className="goal-split">
      <h4>
        <button type="button" className="goal__link" onClick={onOpen}>
          What it went on
        </button>
        <span className="muted">
          <Money value={goal.spent_on_goal} signs="absolute" tone="neutral" /> across{' '}
          {plural(spending, 'transaction')}
        </span>
      </h4>
      <ul>
        {shown.map((one) =>
          row(one.category_id ?? 'uncategorized', one.category_name, one.spent, one.transaction_count),
        )}
        {rest.length === 0
          ? null
          : row(
              'rest',
              plural(rest.length, 'other category', 'other categories'),
              restTotal,
              rest.reduce((total, one) => total + one.transaction_count, 0),
            )}
      </ul>
    </section>
  )
}

function byAccount(goals: Goal[]): [string, Goal[]][] {
  const grouped = new Map<string, Goal[]>()
  for (const goal of goals) {
    const existing = grouped.get(goal.account_name)
    if (existing) existing.push(goal)
    else grouped.set(goal.account_name, [goal])
  }
  return [...grouped.entries()].sort(([a], [b]) => a.localeCompare(b))
}
