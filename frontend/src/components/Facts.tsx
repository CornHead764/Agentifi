import { clsx } from 'clsx'
import type { ReactNode } from 'react'

import { InfoTip } from '@/components/InfoTip'

export interface Fact {
  label: ReactNode
  value: ReactNode
  /** Why the figure is what it is, behind an info button beside the label. */
  info?: ReactNode
  /** Needed only when the label is not a string. */
  id?: string
}

export interface FactsProps {
  /** `null` and `false` entries are skipped, so a conditional row is written inline. */
  facts: readonly (Fact | null | false)[]
  className?: string
}

/** Figures as label and value rows, the value right-aligned. */
export function Facts({ facts, className }: FactsProps) {
  const rows = facts.filter((fact): fact is Fact => Boolean(fact))
  if (rows.length === 0) return null
  return (
    <dl className={clsx('facts', className)}>
      {rows.map((fact, index) => (
        <div
          key={fact.id ?? (typeof fact.label === 'string' ? fact.label : index)}
          className="facts__row"
        >
          <dt>
            {fact.label}
            {fact.info ? <InfoTip>{fact.info}</InfoTip> : null}
          </dt>
          <dd>{fact.value}</dd>
        </div>
      ))}
    </dl>
  )
}
