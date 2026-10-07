/**
 * "Contribute", "Withdraw", "Find rows" and the goal's detail, from a goal's
 * card. A goal counts transactions, never typed amounts: each move is a picker
 * over the last year of rows.
 *
 * The button pressed is what the link records; the sign cannot stand in for
 * it, so it only narrows the list on the account the goal reserves in (see
 * goalCandidates). Contribute and Withdraw look at the goal's own accounts;
 * spending looks at every account.
 */

import { ArrowDownToLine, ArrowUpFromLine, Receipt, Search, X } from 'lucide-react'
import { useMemo, useState } from 'react'

import { GoalFindRows } from '@/components/GoalFindRows'
import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import {
  Button,
  DialogContent,
  EmptyState,
  FormDialog,
  IconButton,
  OptionSelect,
  SearchInput,
  SkeletonRows,
  useHeld,
  useToast,
} from '@/components/ui'
import {
  GOAL_MOVE_LABELS,
  goalCandidates,
  goalCountedMessage,
  goalMoveOfKind,
  useLinkGoalTransaction,
  useUnlinkGoalTransaction,
  type Goal,
  type GoalContribution,
  type GoalMove,
} from '@/lib/goals'
import type { Money as MoneyValue } from '@/lib/money'
import { accountNamer } from '@/lib/accounts'
import { formatDate, toIsoDate } from '@/lib/format'
import { DEFAULT_QUERY } from '@/lib/transactions/api'
import { flattenPages } from '@/lib/transactions/cache'
import { displayPayee } from '@/lib/transactions/edits'
import { useRegister } from '@/lib/transactions/queries'

/** A picker for one move, the goal's detail, or "Find rows" for one kind. */
export type GoalDialogMode = GoalMove | 'history' | `find:${GoalMove}`

const TITLES: Record<GoalMove, string> = {
  contribute: 'Contribute to',
  withdraw: 'Withdraw from',
  spend: 'Spending on',
}

function findMove(mode: GoalDialogMode): GoalMove | null {
  return mode === 'find:contribute'
    ? 'contribute'
    : mode === 'find:withdraw'
      ? 'withdraw'
      : mode === 'find:spend'
        ? 'spend'
        : null
}

function pickerMove(mode: GoalDialogMode): GoalMove | null {
  return mode === 'contribute' || mode === 'withdraw' || mode === 'spend' ? mode : null
}

function titleOf(goal: Goal, mode: GoalDialogMode): string {
  const picking = pickerMove(mode)
  if (picking !== null) return `${TITLES[picking]} ${goal.name}`
  if (findMove(mode) !== null) return `Find rows for ${goal.name}`
  return goal.name
}

const PROMPTS: Record<GoalMove, string> = {
  contribute: 'Pick the transfer or deposit that put the money in. ',
  withdraw: 'Pick the transfer or charge that took the money out. ',
  spend: 'Pick what the money went on. It is recorded against the goal without changing what is set aside; a card charge goes here, not under Withdraw. ',
}

export function GoalTransactionDialog({
  goal: openGoal,
  mode: openMode,
  goals,
  accounts,
  onMode,
  onClose,
}: {
  goal: Goal | null
  mode: GoalDialogMode | null
  /** Every goal, so a row counted toward another one can say so. */
  goals: readonly Goal[]
  accounts: readonly { id: string; name: string }[]
  /** Moves the open dialog to another view of the same goal. */
  onMode: (mode: GoalDialogMode) => void
  onClose: () => void
}) {
  const open = openGoal !== null && openMode !== null
  const goal = useHeld(openGoal)
  const mode = useHeld(openMode)
  const finding = mode === null ? null : findMove(mode)
  const picking = mode === null ? null : pickerMove(mode)
  return (
    <FormDialog open={open} onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        className="goal-txns"
        wide={picking === null}
        title={goal && mode ? titleOf(goal, mode) : ''}
        description={
          goal && mode === 'history' ? (
            'What was saved, what was taken out and what it was spent on. Change what a row is or stop counting it here; the register is unchanged.'
          ) : goal && picking !== null ? (
            <>
              {PROMPTS[picking]}
              {picking === 'spend' ? (
                'Rows on every account, the last year.'
              ) : (
                <>
                  Rows on {goal.account_name}
                  {goal.funding.some((part) => part.account_id !== goal.account_id)
                    ? ' and the accounts funding it'
                    : ''}
                  , the last year.
                </>
              )}
            </>
          ) : undefined
        }
      >
        {goal === null || mode === null ? null : picking !== null ? (
          <Picker
            goal={goal}
            move={picking}
            goals={goals}
            accounts={accounts}
            onClose={onClose}
          />
        ) : finding !== null ? (
          <GoalFindRows goal={goal} move={finding} onMove={(move) => onMode(`find:${move}`)} />
        ) : (
          <Detail goal={goal} onFind={(move) => onMode(`find:${move}`)} />
        )}
      </DialogContent>
    </FormDialog>
  )
}

function Picker({
  goal,
  move,
  goals,
  accounts,
  onClose,
}: {
  goal: Goal
  move: GoalMove
  goals: readonly Goal[]
  accounts: readonly { id: string; name: string }[]
  onClose: () => void
}) {
  const { show } = useToast()
  const moneyText = useMoneyText()
  const link = useLinkGoalTransaction()
  const [search, setSearch] = useState('')

  // A goal with no funding accounts set lists none. Spending asks for null,
  // which the register reads as every account.
  const accountIds = useMemo(() => {
    if (move === 'spend') return null
    const ids = new Set(goal.funding_account_ids)
    ids.add(goal.account_id)
    return [...ids]
  }, [move, goal.account_id, goal.funding_account_ids])
  const [today] = useState(() => new Date())
  const from = useMemo(() => {
    const start = new Date(today)
    start.setFullYear(start.getFullYear() - 1)
    return toIsoDate(start)
  }, [today])
  const register = useRegister({ ...DEFAULT_QUERY, accountIds, from, limit: 200 })
  const rows = useMemo(() => flattenPages(register.data), [register.data])
  const candidates = useMemo(
    () => goalCandidates(rows, move, search, goals, goal),
    [rows, move, search, goals, goal],
  )
  const accountName = useMemo(() => accountNamer(accounts), [accounts])

  const pick = (transactionId: string) => {
    link.mutate(
      { id: goal.id, transactionId, move },
      {
        onSuccess: (updated) => {
          show(goalCountedMessage(updated, move, moneyText))
          onClose()
        },
      },
    )
  }

  return (
    <>
      <SearchInput
        placeholder="Search transactions by payee or whole-dollar amount"
        aria-label="Search transactions"
        value={search}
        onChange={setSearch}
        autoFocus
      />
      {register.isPending ? (
        <SkeletonRows rows={5} />
      ) : candidates.length === 0 ? (
        <EmptyState compact title="Nothing left to count on these accounts in the last year." />
      ) : (
        <ul className="pick-list goal-txns__list">
          {candidates.map(({ row, elsewhere }) => (
            <li key={row.id}>
              <button
                type="button"
                className="pick-list__row"
                disabled={link.isPending || elsewhere !== null}
                onClick={() => pick(row.id)}
              >
                {move === 'contribute' ? (
                  <ArrowDownToLine size={13} aria-hidden="true" />
                ) : move === 'spend' ? (
                  <Receipt size={13} aria-hidden="true" />
                ) : (
                  <ArrowUpFromLine size={13} aria-hidden="true" />
                )}
                <span className="pick-list__name">
                  {displayPayee(row)}
                  <small>
                    {formatDate(row.date, 'short')} · {accountName(row.account_id)}
                    {elsewhere ? ` · already counted toward ${elsewhere.name}` : ''}
                  </small>
                </span>
                <span className="pick-list__figures">
                  <Money value={row.amount} />
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
      {register.hasNextPage ? (
        <p className="link-series__foot muted">
          Showing the newest {rows.length}.{' '}
          <Button
            variant="ghost"
            size="sm"
            disabled={register.isFetchingNextPage}
            onClick={() => void register.fetchNextPage()}
          >
            Load more
          </Button>
        </p>
      ) : null}
    </>
  )
}

/** The detail's three sections, in the order the money moves. */
const SECTIONS: { move: GoalMove; title: string; empty: string }[] = [
  { move: 'contribute', title: 'Saved in', empty: 'Nothing saved in yet.' },
  { move: 'withdraw', title: 'Taken out', empty: 'Nothing taken out yet.' },
  { move: 'spend', title: 'Spent on it', empty: 'No purchases linked yet.' },
]

const MOVE_OPTIONS = SECTIONS.map(({ move }) => ({
  value: move,
  label: GOAL_MOVE_LABELS[move],
}))

/** A section's total is the card's own figure, so the two cannot disagree. */
function sectionTotal(goal: Goal, move: GoalMove): MoneyValue {
  return move === 'contribute' ? goal.funded : move === 'withdraw' ? goal.withdrawn : goal.spent_on_goal
}

/** What one row carried, in its section's terms. */
function rowFigure(row: GoalContribution): MoneyValue {
  return row.kind === 'spending' ? row.spent : row.saved
}

function Detail({ goal, onFind }: { goal: Goal; onFind: (move: GoalMove) => void }) {
  const { show } = useToast()
  const moneyText = useMoneyText()
  const link = useLinkGoalTransaction()
  const unlink = useUnlinkGoalTransaction()
  const busy = link.isPending || unlink.isPending

  return (
    <div className="goal-detail">
      {SECTIONS.map((section) => {
        const rows = goal.contributions.filter((row) => goalMoveOfKind(row.kind) === section.move)
        return (
          <section key={section.move} className="goal-detail__section">
            <header className="goal-detail__head">
              <h3>{section.title}</h3>
              <Money value={sectionTotal(goal, section.move)} signs="absolute" tone="neutral" />
              <Button variant="ghost" size="sm" onClick={() => onFind(section.move)}>
                <Search size={13} /> Find rows
              </Button>
            </header>
            {rows.length === 0 ? (
              <p className="muted">{section.empty}</p>
            ) : (
              <ul className="pick-list goal-detail__rows">
                {rows.map((row) => (
                  <li key={row.transaction_id}>
                    <span className="pick-list__name">
                      {row.payee}
                      <small>
                        {formatDate(row.date, 'short')} · {row.account_name}
                      </small>
                    </span>
                    <span className="pick-list__figures">
                      <Money value={rowFigure(row)} signs="absolute" tone="neutral" />
                    </span>
                    <OptionSelect
                      size="sm"
                      className="goal-detail__kind"
                      aria-label={`What ${row.payee} is`}
                      disabled={busy}
                      value={goalMoveOfKind(row.kind)}
                      onValueChange={(move) =>
                        link.mutate(
                          { id: goal.id, transactionId: row.transaction_id, move },
                          {
                            onSuccess: (updated) =>
                              show(goalCountedMessage(updated, move, moneyText)),
                          },
                        )
                      }
                      options={MOVE_OPTIONS}
                    />
                    <IconButton
                      label={`Stop counting ${row.payee}`}
                      variant="ghost"
                      size="sm"
                      disabled={busy}
                      onClick={() =>
                        unlink.mutate(
                          { id: goal.id, transactionId: row.transaction_id },
                          {
                            onSuccess: (updated) =>
                              show({
                                title: `${row.payee} is off ${goal.name}`,
                                description: `${moneyText(updated.saved_so_far)} still in ${updated.account_name}.`,
                              }),
                          },
                        )
                      }
                    >
                      <X size={13} />
                    </IconButton>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )
      })}
    </div>
  )
}
