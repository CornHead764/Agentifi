import { Target } from 'lucide-react'
import { useState } from 'react'

import { useMoneyText } from '@/components/moneyText'
import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  OptionSelect,
  useToast,
} from '@/components/ui'
import { isOpenGoal, useLinkGoalTransactions, type GoalMove, useGoals } from '@/lib/goals'
import type { Uuid } from '@/lib/transactions/types'

/**
 * "Count toward a goal" for a whole selection. Defaults to spending, since
 * that is what a selection almost always is. Rows already counting toward
 * another goal come back untouched, and the result says so.
 */
export function BulkGoalDialog({
  ids,
  onClose,
  onDone,
}: {
  /** The selection, or an empty set when the dialog is closed. */
  ids: ReadonlySet<Uuid>
  onClose: () => void
  /** Fired once the rows are filed, so the page can clear its selection. */
  onDone: () => void
}) {
  const { show } = useToast()
  const goals = useGoals()
  const link = useLinkGoalTransactions()
  const [move, setMove] = useState<GoalMove>('spend')
  const moneyText = useMoneyText()

  const count = ids.size

  const file = (goalId: string) => {
    link.mutate(
      { id: goalId, transactionIds: [...ids], move },
      {
        onSuccess: ({ goal, linked, skipped }) => {
          onDone()
          onClose()
          show({
            title:
              linked === 0
                ? `Nothing was filed under ${goal.name}`
                : `${linked} ${linked === 1 ? 'row' : 'rows'} filed under ${goal.name}`,
            description:
              skipped.length > 0
                ? `Skipped ${skipped.length}: already on another goal.`
                : move === 'spend'
                  ? `Spent on goal: ${moneyText(goal.spent_on_goal)}. Rows left the spending plan.`
                  : `Saved: ${moneyText(goal.saved_so_far)}.`,
            tone: skipped.length > 0 ? 'error' : undefined,
          })
        },
      },
    )
  }

  return (
    <Dialog open={count > 0} onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        className="bulk-goal"
        footer={<DialogActions onCancel={onClose} cancelDisabled={link.isPending} />}
        title={`Count ${count} ${count === 1 ? 'row' : 'rows'} toward a goal`}
        description={
          move === 'spend'
            ? 'Recorded against the goal. Set-aside is unchanged; the rows leave the spending plan.'
            : 'Contributions and withdrawals move what the goal has set aside.'
        }
      >
        <Field as="group" label="Counts as">
          <OptionSelect
            value={move}
            onValueChange={(next) => setMove(asMove(next))}
            aria-label="Counts as"
            options={[
              { value: 'spend', label: 'Spending on it' },
              { value: 'contribute', label: 'A contribution' },
              { value: 'withdraw', label: 'A withdrawal' },
            ]}
          />
        </Field>

        <QueryBoundary
          query={goals}
          rows={3}
          empty={(all) =>
            !all.some(isOpenGoal) ? (
              <p className="bulk-goal__empty">There are no open goals to count these toward.</p>
            ) : undefined
          }
        >
          {(all) => (
            <ul className="pick-list pick-list--scroll bulk-goal__list">
              {all
                .filter(isOpenGoal)
                .map((one) => (
                  <li key={one.id}>
                    <button
                      type="button"
                      className="pick-list__row bulk-goal__goal"
                      disabled={link.isPending}
                      onClick={() => file(one.id)}
                    >
                      <Target size={14} aria-hidden="true" />
                      <span className="bulk-goal__name">
                        {one.emoji ? `${one.emoji} ` : ''}
                        {one.name}
                      </span>
                      <span className="bulk-goal__meta">
                        {one.account_name} · {moneyText(one.saved_so_far)} saved
                      </span>
                    </button>
                  </li>
                ))}
            </ul>
          )}
        </QueryBoundary>
      </DialogContent>
    </Dialog>
  )
}

// Radix hands back a bare string; this is the one place it becomes a move.
function asMove(value: string): GoalMove {
  return value === 'withdraw' ? 'withdraw' : value === 'contribute' ? 'contribute' : 'spend'
}
