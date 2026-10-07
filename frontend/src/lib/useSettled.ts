import { useEffect, useState } from 'react'

/**
 * A value once it has stopped changing for `delay` milliseconds, so typing
 * "1250" asks one question rather than four.
 */
export function useSettled<T>(value: T, delay = 300): T {
  const [settled, setSettled] = useState(value)

  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delay)
    return () => clearTimeout(timer)
  }, [value, delay])

  return settled
}
