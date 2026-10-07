import { Info } from 'lucide-react'
import type { ReactNode } from 'react'

import { IconButton, Popover, PopoverContent, PopoverTrigger } from '@/components/ui'

export interface InfoTipProps {
  children: ReactNode
  /** The button's accessible name. */
  label?: string
}

/**
 * An explanation kept off the page until asked for.
 *
 * A popover rather than a tooltip: a tooltip never opens on touch, and this
 * holds the only copy of the text.
 */
export function InfoTip({ children, label = 'About this' }: InfoTipProps) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <IconButton label={label} variant="ghost" size="sm" className="info-tip">
          <Info size={12} aria-hidden="true" />
        </IconButton>
      </PopoverTrigger>
      <PopoverContent className="info-tip__body" align="start" side="top">
        {children}
      </PopoverContent>
    </Popover>
  )
}
