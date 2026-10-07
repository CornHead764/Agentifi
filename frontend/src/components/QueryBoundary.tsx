/**
 * The three states every panel can be in. Pending is a skeleton, never a zero;
 * failed says so and offers a retry, never an empty dataset.
 */

import { TriangleAlert } from 'lucide-react'
import type { ReactNode } from 'react'

import { Button, EmptyState, SkeletonRows } from '@/components/ui'
import { describeApiError } from '@/lib/errors'

export interface QueryLike<T> {
  data: T | undefined
  isPending: boolean
  error: unknown
  refetch: () => void
}

export function QueryBoundary<T>({
  query,
  rows = 4,
  empty,
  children,
}: {
  query: QueryLike<T>
  rows?: number
  /** Rendered when the data arrived but has nothing in it. */
  empty?: (data: T) => ReactNode
  children: (data: T) => ReactNode
}) {
  if (query.isPending) return <SkeletonRows rows={rows} />

  if (query.error) return <LoadFailure query={query} />

  if (query.data === undefined) return null

  const emptied = empty?.(query.data)
  return <>{emptied ?? children(query.data)}</>
}

/** A query that failed, with a retry; a page that cannot draw without it shows this in its place. */
export function LoadFailure({
  query,
  title = 'Could not load this',
}: {
  query: Pick<QueryLike<unknown>, 'error' | 'refetch'>
  title?: string
}) {
  return (
    <EmptyState
      icon={<TriangleAlert size={20} />}
      title={title}
      body={describeApiError(query.error, 'load')}
      action={
        <Button size="sm" onClick={() => query.refetch()}>
          Try again
        </Button>
      }
    />
  )
}
