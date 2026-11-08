import { Money } from '@/components/Money'
import { Meter } from '@/components/ui'
import { formatPercentUnits } from '@agentifi/core/format'
import {
  envelopeBarPct,
  envelopeBarSegments,
  envelopeBudget,
  envelopePctUsed,
  envelopeState,
  envelopeTarget,
  type Envelope,
} from '@/lib/spendingPlan'

/**
 * One envelope's bar. The bar fills at 100%; the label does not clamp and can
 * print 250%, so the fill must not be read as the percentage.
 *
 * With rollover carried, the track is the whole budget and the figures name
 * it, as the percentage divides by it. Spend fills from the left; the part
 * past the target was paid from rollover and is drawn solid green, and the
 * rollover still unspent is tinted. All three divide by the same budget so the
 * segments cannot drift apart.
 */
export function EnvelopeBar({ envelope }: { envelope: Envelope }) {
  const state = envelopeState(envelope)
  const pctUsed = envelopePctUsed(envelope)
  const { spentPct, targetPct } = envelopeBarSegments(envelope)
  const label = formatPercentUnits(pctUsed, { digits: 0 })
  // A rollover deep enough in debt leaves no budget to divide by, and the
  // percentage falls back to fully used; the target is then the figure to show.
  const budget = envelopeBudget(envelope)
  const of = budget > 0 ? budget : envelopeTarget(envelope)

  return (
    <div className="envelope-bar" data-state={state}>
      <div className="envelope-bar__figures">
        <span>
          <span className="envelope-bar__lead">Spent </span>
          <Money value={envelope.spent} signs="absolute" tone="neutral" /> of{' '}
          <Money value={of} signs="absolute" tone="neutral" />
        </span>
        {envelope.rollover_amount === 0 ? null : (
          <span
            className="envelope-bar__rollover"
            title={
              envelope.rollover_amount < 0
                ? 'Overspending rolled under from last month, and taken out of the budget'
                : 'Rolled over from last month, and included in the budget'
            }
          >
            <Money value={envelope.rollover_amount} showPlus />
          </span>
        )}
      </div>

      <Meter
        size="lg"
        label={`Spending in ${envelope.name}`}
        value={envelopeBarPct(envelope)}
        valueText={`${label} of ${envelope.name} used`}
        segments={
          state === 'overspent'
            ? [{ value: spentPct, tone: 'overspent' }]
            : envelope.rollover_amount > 0
              ? [
                  { value: Math.min(spentPct, targetPct), tone: 'series' },
                  { value: spentPct - targetPct, tone: 'carried-spent' },
                  { value: targetPct - spentPct, tone: 'none' },
                  { value: 100, tone: 'carried' },
                ]
              : [{ value: spentPct, tone: 'series' }]
        }
        reading={{ text: label, at: spentPct }}
      />
    </div>
  )
}
