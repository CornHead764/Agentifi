import { X } from 'lucide-react'

import type { DrillCrumb } from '@/components/charts'
import { IconButton } from '@/components/ui'

/**
 * The chart's drill steps beside the Filter button, one removable chip each:
 * a wedge narrows the whole register, so what it narrowed to is on the
 * register's toolbar and comes off there.
 */
export function DrillChips({
  crumbs,
  onRemove,
}: {
  crumbs: readonly DrillCrumb[]
  onRemove: (index: number) => void
}) {
  return crumbs.map((crumb, index) => (
    <span key={crumb.key} className="chip chip--on chip--removable">
      {crumb.label}
      <IconButton
        size="sm"
        variant="ghost"
        label={`Remove ${crumb.label} from the filter`}
        onClick={() => onRemove(index)}
      >
        <X size={12} />
      </IconButton>
    </span>
  ))
}
