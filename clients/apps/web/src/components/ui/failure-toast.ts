/** A failure as a toast. A failed mutation is one already, through `MutationFailureToasts`. */

import { useCallback } from 'react'

import { describeApiError } from '@/lib/errors'

import { useToast, type ToastOptions } from './toast-context'

/**
 * A failure as a toast. The title names the action, since several can be in
 * flight; the description is the server's sentence, or ours.
 */
export function failureToast(error: unknown, title = 'That did not work'): ToastOptions {
  return { title, description: describeApiError(error), tone: 'error' }
}

/** Show a failure that is not a mutation's, such as a read, as a toast. */
export function useFailureToast(title?: string): (error: unknown) => void {
  const { show } = useToast()
  return useCallback((error: unknown) => show(failureToast(error, title)), [show, title])
}
