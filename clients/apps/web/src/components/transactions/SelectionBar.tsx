import { CircleCheck, Sparkles, Target, X } from 'lucide-react'

import { Button, IconButton } from '@/components/ui'

export interface SelectionBarProps {
  /** How many rows are about to be acted on. Zero is a state the mode can be in. */
  count: number
  /** Every selected row is already ticked, so the button unticks them instead. */
  allReviewed: boolean
  /** An automation run is being queued; firing a second one would double it. */
  busy: boolean
  onExit: () => void
  onMarkReviewed: () => void
  onSuggestCategories: () => void
  onCountTowardGoal: () => void
}

/**
 * The phone's header while rows are being selected: the count, the bulk
 * actions and the way out. The register's toolbar draws none of them while
 * this is up. Labels hide by stylesheet where they do not fit; every button
 * keeps its accessible name.
 */
export function SelectionBar({
  count,
  allReviewed,
  busy,
  onExit,
  onMarkReviewed,
  onSuggestCategories,
  onCountTowardGoal,
}: SelectionBarProps) {
  // Nothing selected is a real state — clearing the last row does not end the
  // mode — and an action with nothing to act on says so by being unavailable.
  const none = count === 0
  const rows = count === 1 ? 'row' : 'rows'

  return (
    <div className="selection-bar">
      <IconButton label="Exit selection" variant="ghost" onClick={onExit}>
        <X size={16} />
      </IconButton>
      <strong className="selection-bar__count">{count} selected</strong>
      <div className="selection-bar__actions">
        <Button
          variant="ghost"
          className="selection-bar__action"
          disabled={none}
          aria-label={`Mark ${count} ${rows} as ${allReviewed ? 'unreviewed' : 'reviewed'}`}
          onClick={onMarkReviewed}
        >
          <CircleCheck size={16} aria-hidden="true" />
          <span className="selection-bar__label">
            {allReviewed ? 'Mark unreviewed' : 'Mark reviewed'}
          </span>
        </Button>
        <Button
          variant="ghost"
          className="selection-bar__action"
          disabled={none || busy}
          aria-label={`Suggest categories for ${count} ${rows}`}
          onClick={onSuggestCategories}
        >
          <Sparkles size={16} aria-hidden="true" />
          <span className="selection-bar__label">Suggest categories</span>
        </Button>
        <Button
          variant="ghost"
          className="selection-bar__action"
          disabled={none}
          aria-label={`Count ${count} ${rows} toward a goal`}
          onClick={onCountTowardGoal}
        >
          <Target size={16} aria-hidden="true" />
          <span className="selection-bar__label">Count toward a goal</span>
        </Button>
      </div>
    </div>
  )
}
