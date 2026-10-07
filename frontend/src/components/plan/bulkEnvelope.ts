/**
 * One button, many PATCHes, and an honest answer about what happened. The
 * writes go one at a time: each recalculates the whole month, and racing them
 * lets two disagree about the rollover chain. The run does not stop at a
 * failure, and the result counts how many of how many were switched.
 */

import { describeApiError } from '@/lib/errors'

export interface BulkFailure<T> {
  item: T
  error: unknown
}

export interface BulkResult<T> {
  done: T[]
  failed: BulkFailure<T>[]
}

/**
 * Run `write` over every item, in order, without stopping at a failure. Never
 * rejects: the caller decides what a partial run means.
 */
export async function runInOrder<T>(
  items: readonly T[],
  write: (item: T) => Promise<unknown>,
): Promise<BulkResult<T>> {
  const result: BulkResult<T> = { done: [], failed: [] }
  for (const item of items) {
    try {
      await write(item)
      result.done.push(item)
    } catch (error) {
      result.failed.push({ item, error })
    }
  }
  return result
}

export interface BulkToast {
  title: string
  description?: string
  tone: 'success' | 'error'
}

/**
 * What to say afterwards: "N of M" even when everything worked, at most three
 * failures named and the rest counted.
 */
export function describeBulk<T>(
  result: BulkResult<T>,
  label: (item: T) => string,
  verb: string,
): BulkToast {
  const attempted = result.done.length + result.failed.length
  if (result.failed.length === 0) {
    return { title: `${verb} for ${attempted} of ${attempted}`, tone: 'success' }
  }

  const named = result.failed.slice(0, 3)
  const rest = result.failed.length - named.length
  const reasons = named
    .map(({ item, error }) => `${label(item)}: ${describeApiError(error)}`)
    .join(' ')
  const more = rest > 0 ? ` And ${rest} more.` : ''
  return {
    title: `${verb} for ${result.done.length} of ${attempted}`,
    description: `${reasons}${more}`,
    tone: 'error',
  }
}
