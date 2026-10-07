import { ChevronDown, ChevronUp } from 'lucide-react'
import { useState } from 'react'

import { seriesColor } from '@/components/charts/palette'
import { PercentText } from '@/components/figures'
import { Money } from '@/components/Money'
import { Card, ChipGroup, EmptyState, Meter } from '@/components/ui'
import type { AllocationGroup, AllocationSlice } from '@/lib/clients/investments'
import { fractionToPercentUnits, parseRate, plural } from '@/lib/format'
import { addMoney, ZERO_MONEY } from '@/lib/money'

/**
 * What the portfolio is in, from the share the server computes on every
 * holdings read. Bars rather than a donut, so each row reads against its
 * neighbour without a legend.
 *
 * `share` is a fraction; the percent renderers take percent units, so it is
 * converted once here. The tail below 1% folds, and is still counted in the
 * row that folds it.
 */
const TAIL_BELOW_PERCENT = 1

/**
 * Three cuts of the same market value: securities, kinds of thing, and the
 * accounts that hold them, all from one server computation so they cannot
 * disagree.
 */
type View = 'security' | 'class' | 'account'

const VIEWS: { value: View; label: string }[] = [
  { value: 'security', label: 'Security' },
  { value: 'class', label: 'Class' },
  { value: 'account', label: 'Account' },
]

const SUBTITLES: Record<View, string> = {
  security: 'Each security’s share of market value.',
  class: 'Each kind of holding’s share of market value.',
  account: 'Each account’s share of market value.',
}

export function AllocationCard({
  allocation,
  byClass = [],
  byAccount = [],
}: {
  allocation: readonly AllocationSlice[]
  byClass?: readonly AllocationGroup[]
  byAccount?: readonly AllocationGroup[]
}) {
  const [view, setView] = useState<View>('security')
  const groups = view === 'class' ? byClass : byAccount

  return (
    <Card
      title="Allocation"
      subtitle={SUBTITLES[view]}
      actions={
        <ChipGroup
          label="Allocation view"
          layout="tight"
          value={view}
          options={VIEWS}
          onChange={setView}
        />
      }
    >
      {view === 'security' ? (
        <SecurityBars allocation={allocation} />
      ) : (
        <GroupBars groups={groups} />
      )}
    </Card>
  )
}

/** The grouped views, with no folded tail: there are only a few classes and accounts. */
function GroupBars({ groups }: { groups: readonly AllocationGroup[] }) {
  if (groups.length === 0) {
    return (
      <EmptyState
        title="Nothing to allocate"
        body="Portfolio value is zero. Add a holding, or refresh prices."
      />
    )
  }
  return (
    <ul className="allocation">
      {groups.map((group, index) => {
        const percent = fractionToPercentUnits(parseRate(group.share))
        return (
          <li key={group.key}>
            <span className="allocation__symbol">{group.label}</span>
            <Meter size="sm" segments={[{ value: percent ?? 0, color: seriesColor(index) }]} />
            <PercentText rate={percent} tone="neutral" digits={1} />
            <Money value={group.value} signs="absolute" tone="neutral" />
          </li>
        )
      })}
    </ul>
  )
}

function SecurityBars({ allocation }: { allocation: readonly AllocationSlice[] }) {
  const [showTail, setShowTail] = useState(false)

  const rows = allocation.map((slice, index) => ({
    slice,
    index,
    percent: fractionToPercentUnits(parseRate(slice.share)),
  }))
  // Split on position, not value: the list arrives largest first, so a small
  // holding cannot vanish from the middle of it.
  const firstTail = rows.findIndex(({ percent }) => (percent ?? 0) < TAIL_BELOW_PERCENT)
  const head = firstTail === -1 ? rows : rows.slice(0, firstTail)
  const tail = firstTail === -1 ? [] : rows.slice(firstTail)
  const shown = showTail ? rows : head
  const tailValue = tail.reduce((sum, { slice }) => addMoney(sum, slice.value), ZERO_MONEY)

  if (allocation.length === 0) {
    return (
      <EmptyState
        title="Nothing to allocate"
        body="Portfolio value is zero. Add a holding, or refresh prices."
      />
    )
  }

  return (
    <>
      <ul className="allocation">
        {shown.map(({ slice, index, percent }) => (
          <li key={slice.security_id}>
            <span className="allocation__symbol">{slice.symbol}</span>
            <Meter size="sm" segments={[{ value: percent ?? 0, color: seriesColor(index) }]} />
            <PercentText rate={percent} tone="neutral" digits={1} />
            <Money value={slice.value} signs="absolute" tone="neutral" />
          </li>
        ))}
      </ul>
      {tail.length === 0 ? null : (
        <button type="button" className="allocation__tail" onClick={() => setShowTail(!showTail)}>
          {showTail ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
          <span>
            {showTail ? 'Fold' : 'Show'} {plural(tail.length, 'holding')} under {TAIL_BELOW_PERCENT}
            %
          </span>
          <Money value={tailValue} signs="absolute" tone="neutral" />
        </button>
      )}
    </>
  )
}
