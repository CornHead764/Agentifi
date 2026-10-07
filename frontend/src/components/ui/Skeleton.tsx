import { clsx } from 'clsx'
import type { CSSProperties } from 'react'

interface SkeletonProps {
  width?: CSSProperties['width']
  height?: CSSProperties['height']
  className?: string
}

/** A loading placeholder, never a placeholder number: a figure drawn before its data is indistinguishable from a wrong one. */
function Skeleton({ width = '100%', height = 12, className }: SkeletonProps) {
  return (
    <span
      className={clsx('skeleton', className)}
      style={{ width, height }}
      aria-hidden="true"
    />
  )
}

export function SkeletonRows({ rows = 4, className }: { rows?: number; className?: string }) {
  return (
    <div className={className} role="status" aria-label="Loading">
      {Array.from({ length: rows }, (_, index) => (
        <Skeleton key={index} width={index % 3 === 2 ? '60%' : '100%'} className="skeleton-row" />
      ))}
    </div>
  )
}
