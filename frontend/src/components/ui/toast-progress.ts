import { useMemo, type ReactNode } from 'react'

import { failureToast } from './failure-toast'
import { useToastOrNull, type ToastOptions, type ToastValue } from './toast-context'

/** What came of the work, fixed or read off its result. */
export type ProgressOutcome<T> = ToastOptions | ((result: T) => ToastOptions)

/**
 * What a failure says: a title over the server's sentence, or the whole toast.
 * Left out, it is `failureToast`'s own title over that sentence.
 */
export type ProgressFailure = string | ((error: unknown) => ToastOptions)

/**
 * The one way long-running work reports itself: a loading toast for as long
 * as `work` runs, replaced where it stands by what came of it, success or
 * failure; there is no second toast for either. It resolves to the result, or
 * to undefined once a failure has been shown, and never rejects. It holds no
 * component state, so it finishes after whatever started it has unmounted.
 *
 * The mutation behind `work` must not have a failure handler of its own, or a
 * failure is shown twice.
 */
export async function withProgressToast<T>(
  toast: Pick<ToastValue, 'show' | 'update'>,
  loading: ReactNode,
  work: Promise<T>,
  outcome: ProgressOutcome<T>,
  failure?: ProgressFailure,
): Promise<T | undefined> {
  const id = toast.show({ title: loading, tone: 'loading' })
  let result: T
  try {
    result = await work
  } catch (error) {
    toast.update(id, typeof failure === 'function' ? failure(error) : failureToast(error, failure))
    return undefined
  }
  toast.update(id, typeof outcome === 'function' ? outcome(result) : outcome)
  return result
}

export type RunWithProgress = <T>(
  loading: ReactNode,
  work: Promise<T>,
  outcome: ProgressOutcome<T>,
  failure?: ProgressFailure,
) => Promise<T | undefined>

const SILENT: Pick<ToastValue, 'show' | 'update'> = { show: () => 0, update: () => {} }

/**
 * `withProgressToast` bound to this tree's toasts. Outside a `ToastProvider`,
 * as in a component's own test, the work still runs and nothing is shown.
 */
export function useProgressToast(): RunWithProgress {
  const toast = useToastOrNull()
  return useMemo(() => {
    const run: RunWithProgress = (loading, work, outcome, failure) =>
      withProgressToast(toast ?? SILENT, loading, work, outcome, failure)
    return run
  }, [toast])
}
