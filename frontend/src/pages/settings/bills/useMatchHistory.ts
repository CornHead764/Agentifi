import { useProgressToast } from '@/components/ui'
import {
  describeBillHistory,
  useMatchBillHistory,
  type BillHistoryScope,
} from '@/lib/clients/bills'

/**
 * Matches a provider's bills, or one billed account's, against every past
 * payment of the linked reminder, and says what it found in a toast.
 */
export function useMatchHistory(provider: string) {
  const match = useMatchBillHistory()
  const progress = useProgressToast()
  const run = (scope: BillHistoryScope) =>
    progress(
      `Matching ${provider} bills to past payments…`,
      match.mutateAsync(scope),
      (result) => describeBillHistory(result, provider),
      `${provider} bills were not matched`,
    )
  return { run, pending: match.isPending }
}
