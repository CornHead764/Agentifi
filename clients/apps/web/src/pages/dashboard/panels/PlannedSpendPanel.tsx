import { EnvelopeBar } from '@/components/plan/EnvelopeBar'
import { EnvelopeName, EnvelopeReadout } from '@/components/plan/EnvelopePanel'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Card, EmptyState } from '@/components/ui'
import { useSpendingPlanMonth } from '@/lib/clients/dashboard'

/** The first envelopes of the month, drawn as the spending plan's card view draws them. */
export function PlannedSpendPanel() {
  const month = useSpendingPlanMonth()
  return (
    <QueryBoundary query={month} rows={4}>
      {(data) =>
        data.envelopes.length === 0 ? (
          <EmptyState title="No planned expenses yet" body="Set a monthly target for a category." />
        ) : (
          <div className="cards">
            {data.envelopes.slice(0, 4).map((envelope) => (
              <Card
                key={envelope.id}
                className="envelope-card"
                title={<EnvelopeName envelope={envelope} />}
              >
                <div className="stack stack--2">
                  <EnvelopeBar envelope={envelope} />
                  <EnvelopeReadout envelope={envelope} />
                </div>
              </Card>
            ))}
          </div>
        )
      }
    </QueryBoundary>
  )
}
