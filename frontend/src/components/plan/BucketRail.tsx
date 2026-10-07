import { Equal, Minus, Plus } from 'lucide-react'

import { Facts } from '@/components/Facts'
import { Money } from '@/components/Money'
import { Badge, Card, List, ListRow, Tooltip } from '@/components/ui'
import {
  BUCKET_LABELS,
  MONTH_HEADLINES,
  STACKED_BUCKETS,
  effectiveAmount,
  isOverridden,
  monthPhase,
  projectionMethod,
  shiftMonth,
  type BucketKey,
  type SpendingPlanMonth,
} from '@/lib/spendingPlan'
import { formatMonthKey, plural } from '@/lib/format'

export interface BucketRailProps {
  month: SpendingPlanMonth
  selected: BucketKey
  onSelect: (bucket: BucketKey) => void
}

/**
 * The month's five buckets, the one number they resolve to, and what rolled
 * in. The headline is the month's own result; what rolled in is its own line
 * under it, added, so the arithmetic on screen closes to the engine's figure.
 * Anything that changes the headline without a line (a projection, an
 * override) is labelled where it lands.
 */
export function BucketRail({ month, selected, onSelect }: BucketRailProps) {
  const phase = monthPhase(month)
  const result = month.month_result
  const projected = month.projected_month_result
  const headline = phase === 'future' ? projected : result
  const rate = month.month_result_per_day
  const rollover = effectiveAmount(month.buckets.rollover)
  const withRollover = phase === 'past' ? month.left_this_month : month.projected_left

  return (
    <Card flush className="plan-rail">
      <List>
        {STACKED_BUCKETS.map((key) => {
          const bucket = month.buckets[key]
          return (
            <ListRow
              key={key}
              current={key === selected}
              onSelect={() => onSelect(key)}
              title={
                <>
                  <span className="plan-bucket__glyph" data-bucket={key} aria-hidden="true">
                    {key === 'income' ? <Plus size={13} /> : <Minus size={13} />}
                  </span>
                  {BUCKET_LABELS[key]}
                </>
              }
              badge={
                isOverridden(bucket) ? (
                  <Tooltip label="A user override, not the calculated figure" side="top">
                    <Badge tone="warning">edited</Badge>
                  </Tooltip>
                ) : null
              }
              figures={<Money value={effectiveAmount(bucket)} showPlus={key === 'income'} />}
            />
          )
        })}
      </List>

      <div className="plan-rail__total">
        <span className="plan-bucket__glyph" data-bucket="total" aria-hidden="true">
          <Equal size={13} />
        </span>
        <div className="plan-rail__totals">
          <p className="plan-rail__label hint">{MONTH_HEADLINES[phase]}</p>
          <Money value={headline} className="plan-rail__headline figure--total" />
          <Facts
            className="plan-rail__facts"
            facts={[
              phase === 'current' && {
                label: rate === null ? 'Per day' : `Per day, ${plural(month.days_remaining, 'day')} left`,
                value: rate === null ? '—' : <Money value={rate} />,
                info: rate === null ? 'No days left to spread it across.' : undefined,
              },
              rollover !== 0 && {
                label: `Rolled over from ${formatMonthKey(shiftMonth(month.month, -1), 'month')}`,
                value: <Money value={rollover} showPlus />,
              },
              rollover !== 0 && {
                label: 'With rollover',
                value: <Money value={withRollover} />,
              },
            ]}
          />
        </div>
      </div>

      {phase === 'past' ? null : (
        <div className="plan-rail__projection">
          {phase === 'current' ? (
            <>
              <p className="plan-rail__label hint">Projected to end the month</p>
              <Money value={projected} className="plan-rail__projected" />
            </>
          ) : null}
          <Facts
            className="plan-rail__facts"
            facts={[
              {
                label: 'Other Spend, projected',
                value: (
                  <Money value={month.projected_other_spending} signs="absolute" tone="neutral" />
                ),
              },
              { label: 'Method', value: projectionMethod(month.projection, month) },
              month.projection.buffer !== 0 && {
                label: 'Buffer',
                value: <Money value={month.projection.buffer} signs="absolute" tone="neutral" />,
              },
            ]}
          />
        </div>
      )}
    </Card>
  )
}
