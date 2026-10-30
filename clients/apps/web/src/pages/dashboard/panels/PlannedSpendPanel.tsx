import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Card, EmptyState, Meter, type MeterTone } from '@/components/ui'
import { useSpendingPlanMonth } from '@/lib/clients/dashboard'
import {
  envelopeAvailable,
  envelopeBarPct,
  envelopeState,
  envelopeTarget,
  type EnvelopeState,
} from '@/lib/spendingPlan'

const STATE_TONES: Record<EnvelopeState, MeterTone> = {
  normal: 'accent',
  with_rollover: 'income',
  overspent: 'expense',
}

export function PlannedSpendPanel() {
  const month = useSpendingPlanMonth()
  return (
    <QueryBoundary query={month} rows={4}>
      {(data) =>
        data.envelopes.length === 0 ? (
          <EmptyState title="No planned expenses yet" body="Set a monthly target for a category." />
        ) : (
          <div className="cards">
            {data.envelopes.slice(0, 4).map((envelope) => {
              const state = envelopeState(envelope)
              return (
                <Card key={envelope.id} title={envelope.name}>
                  <div className="stack stack--2">
                    <span>
                      <Money
                        value={envelopeAvailable(envelope)}
                        signs="absolute"
                        tone={state === 'overspent' ? 'signed' : 'neutral'}
                      />{' '}
                      <span className="muted">
                        {state === 'overspent' ? 'overspent' : 'available'}
                      </span>
                    </span>
                    {/* The label is uncapped — 250% is a real reading — but the
                        bar fills at 100. */}
                    <Meter
                      label={`Share of ${envelope.name} spent`}
                      segments={[{ value: envelopeBarPct(envelope), tone: STATE_TONES[state] }]}
                    />
                    <span className="muted">
                      Spent <Money value={envelope.spent} signs="absolute" tone="neutral" /> of{' '}
                      <Money value={envelopeTarget(envelope)} signs="absolute" tone="neutral" />
                      {envelope.rollover_amount > 0 ? (
                        <>
                          {' '}
                          <Money value={envelope.rollover_amount} showPlus />
                        </>
                      ) : null}
                    </span>
                  </div>
                </Card>
              )
            })}
          </div>
        )
      }
    </QueryBoundary>
  )
}
