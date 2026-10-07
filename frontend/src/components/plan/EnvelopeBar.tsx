import { Money } from '@/components/Money'
import { Meter } from '@/components/ui'
import { formatPercentUnits } from '@/lib/format'
import {
  envelopeBarPct,
  envelopeBarSegments,
  envelopePctUsed,
  envelopeState,
  envelopeTarget,
  type Envelope,
} from '@/lib/spendingPlan'

/**
 * One envelope's bar. The bar fills at 100%; the label does not clamp and can
 * print 250%, so the fill must not be read as the percentage.
 *
 * With rollover carried, the track is the whole budget: spend fills from the
 * left, the slice past the target is rollover, and both divide by the same
 * budget so the segments cannot drift apart.
 */
export function EnvelopeBar({ envelope }: { envelope: Envelope }) {
  const state = envelopeState(envelope)
  const pctUsed = envelopePctUsed(envelope)
  const { spentPct, targetPct } = envelopeBarSegments(envelope)
  const label = formatPercentUnits(pctUsed, { digits: 0 })

  return (
    <div className="envelope-bar" data-state={state}>
      <div className="envelope-bar__figures">
        <span>
          Spent <Money value={envelope.spent} signs="absolute" tone="neutral" />
          {state === 'overspent' ? null : (
            <>
              {' of '}
              <Money value={envelopeTarget(envelope)} signs="absolute" tone="neutral" />
            </>
          )}
        </span>
        {envelope.rollover_amount === 0 ? (
          state === 'overspent' ? (
            <span>
              of <Money value={envelopeTarget(envelope)} signs="absolute" tone="neutral" />
            </span>
          ) : null
        ) : (
          <span className="envelope-bar__rollover">
            <Money value={envelope.rollover_amount} showPlus />
          </span>
        )}
      </div>

      <Meter
        size="lg"
        label={`Spending in ${envelope.name}`}
        value={envelopeBarPct(envelope)}
        valueText={`${label} of ${envelope.name} used`}
        segments={[
          { value: spentPct, tone: state === 'overspent' ? 'overspent' : 'series' },
          ...(envelope.rollover_amount > 0
            ? [
                { value: targetPct - spentPct, tone: 'none' as const },
                { value: 100, tone: 'carried' as const },
              ]
            : []),
        ]}
        reading={{ text: label, at: spentPct }}
      />
    </div>
  )
}
