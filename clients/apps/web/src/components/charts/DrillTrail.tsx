import { ChevronRight } from 'lucide-react'

export interface DrillCrumb {
  key: string
  label: string
}

/**
 * The breadcrumb over a drilled chart: the root, then each level drilled
 * through, the last one being where the chart is. `onStep(0)` is the root and
 * `onStep(n)` the n-th crumb. Draws nothing at the root, where there is
 * nowhere to go back to.
 */
export function DrillTrail({
  label,
  root,
  trail,
  onStep,
}: {
  /** The navigation landmark's name. */
  label: string
  root: string
  trail: readonly DrillCrumb[]
  onStep: (depth: number) => void
}) {
  if (trail.length === 0) return null
  return (
    <nav className="drill" aria-label={label}>
      <button type="button" className="drill__step" onClick={() => onStep(0)}>
        {root}
      </button>
      {trail.map((crumb, index) => (
        <span className="drill__step-group" key={crumb.key}>
          <ChevronRight size={13} className="drill__arrow" aria-hidden="true" />
          {index === trail.length - 1 ? (
            <span className="drill__here" aria-current="true">
              {crumb.label}
            </span>
          ) : (
            <button type="button" className="drill__step" onClick={() => onStep(index + 1)}>
              {crumb.label}
            </button>
          )}
        </span>
      ))}
    </nav>
  )
}
