import { useNavigate } from 'react-router-dom'

import { Donut, DonutGroup, DonutLegend } from '@/components/charts'
import { Facts } from '@/components/Facts'
import { MoneyOrDash } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { useSpendingPlanMonth } from '@/lib/clients/dashboard'
import { plural } from '@/lib/format'
import {
  BUCKET_LABELS,
  BUCKET_PATHS,
  MONTH_HEADLINES,
  STACKED_BUCKETS,
  effectiveAmount,
  monthPhase,
} from '@/lib/spendingPlan'

/**
 * Each outgoing bucket's route, widened to a plain lookup: the donut hands a
 * slice key back as a string.
 */
const BUCKET_ROUTES: Readonly<Record<string, string>> = BUCKET_PATHS


/**
 * The month, in the plan rail's own figures and words. The headline leaves
 * the rollover out, so it is its own line here too.
 */
export function SpendingPlanPanel() {
  const month = useSpendingPlanMonth()
  const navigate = useNavigate()
  return (
    <QueryBoundary query={month} rows={4}>
      {(data) => {
        const result = data.month_result
        const rate = data.month_result_per_day
        const rollover = effectiveAmount(data.buckets.rollover)
        // Where the month went, rather than one slice, which would draw a full
        // ring at every value. The outgoing buckets carry the rail's colours.
        const slices = STACKED_BUCKETS.filter((key) => key !== 'income')
          .map((key) => ({
            key,
            label: BUCKET_LABELS[key],
            value: effectiveAmount(data.buckets[key]),
            // The plan rail's own bucket colour, so the ring and the page agree.
            color: `var(--bucket-${key})`,
            // Every outgoing bucket has a route of its own, and the plan page
            // is where the rows behind the wedge are listed.
            action: `Open ${BUCKET_LABELS[key]} in the spending plan`,
          }))
          .filter((slice) => slice.value !== 0)
        return (
          <>
            {/* The figure first, across the card: beside the ring a line
                such as "−$40.00 per day · 12 days left" would wrap. */}
            <div>
              <p className="widget__lead">{MONTH_HEADLINES[monthPhase(data)]}</p>
              <p className="widget__headline">
                <Money value={result} />
              </p>
              <Facts
                facts={[
                  {
                    label: `Per day, ${plural(data.days_remaining, 'day')} left`,
                    value: (
                      <MoneyOrDash
                        value={rate}
                        tone="neutral"
                        reason="No days left in the month to spread it over."
                      />
                    ),
                  },
                  rollover !== 0 && {
                    label: 'Rolled over',
                    value: <Money value={rollover} showPlus />,
                  },
                  rollover !== 0 && {
                    label: 'With rollover',
                    value: <Money value={data.left_this_month} />,
                  },
                ]}
              />
            </div>

            {/* `donut-row`, not `plan`: `.plan` is the spending plan page's
                grid. */}
            <DonutGroup
              className="donut-row donut-row--tight"
              onSelect={(slice) => navigate(`/spending-plan/${BUCKET_ROUTES[slice.key] ?? ''}`)}
            >
              <Donut height={150} signs="stored" slices={slices} />
              <DonutLegend signs="stored" slices={slices} />
            </DonutGroup>
          </>
        )
      }}
    </QueryBoundary>
  )
}
