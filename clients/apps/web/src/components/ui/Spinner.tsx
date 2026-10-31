import { clsx } from 'clsx'
import { Loader2 } from 'lucide-react'

export interface SpinnerProps {
  size?: number
  /** Names the wait for a screen reader. Without one it is decoration beside words that already say it. */
  label?: string
  className?: string
}

/** The one sign that work is still running. */
export function Spinner({ size = 14, label, className }: SpinnerProps) {
  return (
    <Loader2
      size={size}
      className={clsx('spinner', className)}
      {...(label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': true })}
    />
  )
}
