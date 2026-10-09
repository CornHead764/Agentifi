import { useEffect, useState } from 'react'

import type { MerchantPullProgress } from '@/lib/clients/merchant'
import { formatElapsed } from '@/lib/clients/pulling'

/**
 * What a running pull is doing now, and for how long it has been at it. The
 * server writes the line; the clock ticks here, so the elapsed time moves
 * between the polls that bring a new line. With no line yet, `fallback`.
 */
export function PullProgressLine({
  progress,
  fallback,
  now,
}: {
  progress: MerchantPullProgress | null | undefined
  fallback: string
  /** A fixed clock, for a render that cannot tick. */
  now?: number
}) {
  const [tick, setTick] = useState(() => Date.now())
  useEffect(() => {
    if (now !== undefined || !progress) return
    const timer = setInterval(() => setTick(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [now, progress])

  if (!progress) return <>{fallback}</>
  const elapsed = (now ?? tick) - new Date(progress.started_at).getTime()
  return (
    <>
      {progress.line} ({formatElapsed(elapsed)})
    </>
  )
}
