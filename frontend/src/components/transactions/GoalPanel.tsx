import { Target } from 'lucide-react'
import { useState } from 'react'

import { useMoneyText } from '@/components/moneyText'
import { Button, Field, OptionSelect, useToast } from '@/components/ui'
import {
  goalCountedMessage,
  goalCounting,
  isOpenGoal,
  useLinkGoalTransaction,
  useUnlinkGoalTransaction,
  type Goal,
  type GoalMove,
  useGoals,
} from '@/lib/goals'
import type { Transaction } from '@/lib/transactions/types'

/**
 * The goal a saved row counts toward, from the row's own detail. A row counts
 * toward one goal, and which way it counts is recorded rather than read off
 * its sign; the sign only picks the starting answer. Moving to another goal is
 * stop, then pick. A zero row gets no field.
 */
export function GoalPanel({ transaction }: { transaction: Transaction }) {
  const { show } = useToast()
  const goals = useGoals()
  const link = useLinkGoalTransaction()
  const unlink = useUnlinkGoalTransaction()
  const [chosen, setChosen] = useState<GoalMove | null>(null)
  const moneyText = useMoneyText()

  const { goal, move } = goalCounting(transaction, goals.data ?? [])
  const open = (goals.data ?? []).filter((one) => isOpenGoal(one) || one.id === goal?.id)
  if (move === null || goals.isPending || (open.length === 0 && goal === null)) return null
  const busy = link.isPending || unlink.isPending
  // For a counted row the goal's own answer wins on every refetch; `chosen` only
  // carries the direction of a link that has not been made yet.
  const direction = goal ? move : (chosen ?? move)

  const counted = (updated: Goal, way: GoalMove) =>
    show(goalCountedMessage(updated, way, moneyText))

  const chooseDirection = (next: GoalMove) => {
    setChosen(next)
    if (goal) {
      link.mutate(
        { id: goal.id, transactionId: transaction.id, move: next },
        { onSuccess: (updated) => counted(updated, next) },
      )
    }
  }

  const directionField = (
    <OptionSelect
      value={direction}
      disabled={busy}
      onValueChange={(next) => chooseDirection(asMove(next))}
      aria-label="Counts as"
      className="txn-goal__direction"
      options={[
        { value: 'contribute', label: 'A contribution' },
        { value: 'withdraw', label: 'A withdrawal' },
        { value: 'spend', label: 'Spending on it' },
      ]}
    />
  )

  return (
    <Field
      as="group"
      label="Savings goal"
      hint={
        goal
          ? direction === 'spend'
            ? `Spent so far: ${moneyText(goal.spent_on_goal)}`
            : `Saved so far: ${moneyText(goal.saved_so_far)}`
          : 'Pick a goal for this row to count toward.'
      }
    >
      {goal ? (
        <div className="txn-goal">
          <span className="txn-goal__name">
            <Target size={13} aria-hidden="true" /> {goal.emoji ? `${goal.emoji} ` : ''}
            {goal.name}
          </span>
          {directionField}
          <Button
            variant="ghost"
            size="sm"
            disabled={busy}
            onClick={() =>
              unlink.mutate(
                { id: goal.id, transactionId: transaction.id },
                {
                  onSuccess: (updated) =>
                    show({
                      title: `No longer counted toward ${goal.name}`,
                      description: `${moneyText(updated.saved_so_far)} saved so far.`,
                    }),
                },
              )
            }
          >
            Stop counting
          </Button>
        </div>
      ) : (
        <div className="txn-goal txn-goal--picking">
          <OptionSelect
            value=""
            disabled={busy}
            onValueChange={(id) => {
              const picked = open.find((one) => one.id === id)
              if (!picked) return
              link.mutate(
                { id, transactionId: transaction.id, move: direction },
                { onSuccess: (updated) => counted(updated, direction) },
              )
            }}
            placeholder="Count toward a goal…"
            options={open.map((one) => ({
              value: one.id,
              label: `${one.emoji ? `${one.emoji} ` : ''}${one.name} · ${one.account_name}`,
            }))}
          />
        </div>
      )}
    </Field>
  )
}

// Radix hands back a bare string; this is the one place it becomes a move.
function asMove(value: string): GoalMove {
  return value === 'withdraw' ? 'withdraw' : value === 'spend' ? 'spend' : 'contribute'
}
