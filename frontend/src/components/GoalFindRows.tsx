/**
 * "Find rows" for one goal: the rows of one kind it is likely missing, ranked
 * by the server (domain.SuggestGoalRows), to tick and file in one bulk link.
 * Nothing is filed until the user says so; a suggestion is a guess.
 */

import { useMemo, useRef, useState } from 'react'

import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { Button, Checkbox, ChipGroup, EmptyState, SkeletonRows, useToast } from '@/components/ui'
import { formatDate, plural } from '@/lib/format'
import {
  describeGoalSuggestion,
  useGoalSuggestions,
  useLinkGoalTransactions,
  type Goal,
  type GoalMove,
} from '@/lib/goals'
import { rangeToggled } from '@/lib/selection'

const KINDS: { value: GoalMove; label: string }[] = [
  { value: 'spend', label: 'Spending' },
  { value: 'withdraw', label: 'Taken out' },
  { value: 'contribute', label: 'Saved in' },
]

const EXPLAINED: Record<GoalMove, string> = {
  spend:
    'Purchases on any account around the withdrawals, most like the spending already on the goal first. Transfers and card payments are left out.',
  withdraw: 'Money leaving the goal’s account after it was first saved into.',
  contribute: 'Money arriving in the goal’s account before any was taken out.',
}

const ADDED_AS: Record<GoalMove, string> = {
  spend: 'spending',
  withdraw: 'taken out',
  contribute: 'saved in',
}

export function GoalFindRows({
  goal,
  move,
  onMove,
}: {
  goal: Goal
  move: GoalMove
  onMove: (move: GoalMove) => void
}) {
  return (
    <div className="stack stack--3">
      <ChipGroup label="Look for" value={move} options={KINDS} onChange={onMove} layout="tight" />
      <p className="muted">{EXPLAINED[move]}</p>
      {/* A fresh list per kind, so a selection never carries across kinds. */}
      <Suggestions key={move} goal={goal} move={move} />
    </div>
  )
}

function Suggestions({ goal, move }: { goal: Goal; move: GoalMove }) {
  const { show } = useToast()
  const moneyText = useMoneyText()
  const suggestions = useGoalSuggestions(goal.id, move)
  const link = useLinkGoalTransactions()
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const anchor = useRef<string | null>(null)

  const rows = useMemo(() => suggestions.data?.rows ?? [], [suggestions.data])
  const order = useMemo(() => rows.map((row) => row.transaction_id), [rows])
  const chosen = order.filter((id) => selected.has(id))

  if (suggestions.isPending) return <SkeletonRows rows={5} />
  if (suggestions.isError) {
    return <EmptyState compact title="Suggestions could not be loaded." />
  }
  if (rows.length === 0) {
    return (
      <EmptyState
        compact
        title={
          suggestions.data.from === null
            ? 'Count a row toward this goal first; suggestions are found around its own rows.'
            : 'Nothing else looks like it belongs to this goal.'
        }
      />
    )
  }

  const all = chosen.length === order.length
  const click = (id: string, extend: boolean) => {
    setSelected(rangeToggled(order, selected, anchor.current, id, extend))
    anchor.current = id
  }

  const add = () => {
    link.mutate(
      { id: goal.id, transactionIds: chosen, move },
      {
        onSuccess: ({ goal: updated, linked, skipped }) => {
          setSelected(new Set())
          anchor.current = null
          show({
            title: `${plural(linked, 'row')} added to ${updated.name} as ${ADDED_AS[move]}`,
            description:
              skipped.length > 0
                ? `Skipped ${skipped.length}: already on another goal.`
                : move === 'spend'
                  ? `${moneyText(updated.spent_on_goal)} spent on it.`
                  : `${moneyText(updated.saved_so_far)} still in ${updated.account_name}.`,
            tone: skipped.length > 0 ? 'error' : undefined,
          })
        },
      },
    )
  }

  return (
    <>
      <div className="goal-find__head">
        <Checkbox
          label={all ? 'Clear all' : `Select all ${order.length}`}
          checked={all ? true : chosen.length > 0 ? 'indeterminate' : false}
          onCheckedChange={() => setSelected(all ? new Set() : new Set(order))}
        />
        {suggestions.data.from && suggestions.data.to ? (
          <small className="muted">
            {formatDate(suggestions.data.from, 'short')} to {formatDate(suggestions.data.to, 'short')}
          </small>
        ) : null}
      </div>
      <ul className="pick-list pick-list--scroll goal-find__list">
        {rows.map((row) => (
          <li key={row.transaction_id}>
            <Checkbox
              aria-label={`Add ${row.payee}`}
              checked={selected.has(row.transaction_id)}
              onClick={(event) => click(row.transaction_id, event.shiftKey)}
            />
            <span className="pick-list__name">
              {row.payee}
              <small>{describeGoalSuggestion(row)}</small>
            </span>
            <span className="pick-list__figures">
              <Money value={row.amount} />
            </span>
          </li>
        ))}
      </ul>
      {suggestions.data.truncated ? (
        <p className="muted">Showing the best {rows.length}. Add these, and the rest come next.</p>
      ) : null}
      <div className="goal-find__actions">
        <Button variant="primary" disabled={chosen.length === 0 || link.isPending} onClick={add}>
          {chosen.length === 0
            ? 'Tick the rows to add'
            : `Add ${plural(chosen.length, 'row')} as ${ADDED_AS[move]}`}
        </Button>
      </div>
    </>
  )
}
