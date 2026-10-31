import { ChevronDown, ChevronUp } from 'lucide-react'
import { useState } from 'react'

import { readStoredFlag, writeStoredFlag } from '@/lib/storage'

import { Card, type CardProps } from './Card'
import { IconButton } from './Button'

export interface CollapsibleCardProps extends CardProps {
  /** Where the collapsed state is remembered, per browser. */
  storageKey: string
  startCollapsed?: boolean
  /** What the toggle shows and hides: "Show {what}", "Hide {what}". */
  what: string
}

/**
 * A card whose body folds away under a toggle at the end of its actions. The
 * header stays, so the title and subtitle still read while it is folded.
 */
export function CollapsibleCard({
  storageKey,
  startCollapsed = false,
  what,
  actions,
  children,
  ...props
}: CollapsibleCardProps) {
  const [collapsed, setCollapsed] = useState(() => readStoredFlag(storageKey, startCollapsed))
  const toggle = () => {
    const next = !collapsed
    setCollapsed(next)
    writeStoredFlag(storageKey, next)
  }

  return (
    <Card
      {...props}
      actions={
        <>
          {actions}
          <IconButton
            label={collapsed ? `Show ${what}` : `Hide ${what}`}
            size="sm"
            variant="ghost"
            aria-expanded={!collapsed}
            onClick={toggle}
          >
            {collapsed ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
          </IconButton>
        </>
      }
    >
      {collapsed ? null : children}
    </Card>
  )
}
