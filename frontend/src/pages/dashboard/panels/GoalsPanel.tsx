import { Link } from 'react-router-dom'

import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Button, Card, EmptyState, Meter, type MeterSegment } from '@/components/ui'
import { goalBarSegments, isOpenGoal, useGoals, type Goal } from '@/lib/goals'

function goalMeterSegments(goal: Goal): MeterSegment[] {
  const { saved, spent } = goalBarSegments(goal)
  return [{ value: saved }, { value: spent, faded: true }]
}

export function GoalsPanel() {
  const goals = useGoals()
  return (
    <QueryBoundary query={goals} rows={3}>
      {(all) => {
        const rows = all.filter(isOpenGoal)
        return rows.length === 0 ? (
          <EmptyState
            title="No goals yet"
            body="Save toward future plans or irregular expenses."
            action={
              <Button asChild variant="primary" size="sm">
                <Link to="/goals">New goal</Link>
              </Button>
            }
          />
        ) : (
          <div className="cards">
            {rows.slice(0, 3).map((goal) => (
              <Card
                key={goal.id}
                title={
                  <>
                    {goal.emoji ? <span aria-hidden="true">{goal.emoji}</span> : null} {goal.name}
                  </>
                }
              >
                <div className="stack stack--2">
                  <Meter
                    label={`Progress toward ${goal.name}`}
                    segments={goalMeterSegments(goal)}
                  />
                  {goal.stage === 'saving' ? (
                    <span className="muted">
                      <Money value={goal.saved_so_far} tone="neutral" /> of{' '}
                      <Money value={goal.target_amount} tone="neutral" />
                    </span>
                  ) : (
                    <span className="muted">
                      Saved <Money value={goal.funded} tone="neutral" />, spent{' '}
                      <Money value={goal.spent_on_goal} signs="absolute" tone="neutral" />
                    </span>
                  )}
                </div>
              </Card>
            ))}
          </div>
        )
      }}
    </QueryBoundary>
  )
}
