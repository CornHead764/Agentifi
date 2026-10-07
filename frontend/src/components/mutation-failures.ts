import type { QueryClient } from '@tanstack/react-query'

import { failureToast, type ToastOptions } from '@/components/ui'

/**
 * Every failed mutation on `client` becomes a toast unless its `meta.failure`
 * is false. Returns the unsubscribe.
 */
export function toastFailedMutations(
  client: QueryClient,
  show: (toast: ToastOptions) => void,
): () => void {
  return client.getMutationCache().subscribe((event) => {
    if (event.type !== 'updated' || event.action.type !== 'error') return
    const title = event.mutation.meta?.failure
    if (title === false) return
    show(failureToast(event.action.error, title ?? 'That change did not save'))
  })
}
