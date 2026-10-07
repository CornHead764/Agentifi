import { useMoneyText } from '@/components/moneyText'
import { useProgressToast, type RunWithProgress } from '@/components/ui'
import {
  useRepricing,
  useRevalueAsset,
  useValuationSources,
  type ValuationResults,
} from '@/lib/clients/connections'
import {
  canReprice,
  repricingTitle,
  revaluationFailureTitle,
  singleRevaluationResults,
} from '@/lib/revaluation'
import type { MoneyFormatter } from '@/lib/money'
import type { AccountWithBalances } from '@/lib/transactions/types'

type Repriced = Pick<AccountWithBalances, 'name' | 'currency'>

/**
 * One asset's re-price as a loading toast that becomes its result: what it was
 * priced as, or why it was not.
 */
export function repriceWithToast(
  progress: RunWithProgress,
  account: Repriced,
  work: Promise<ValuationResults>,
  format?: MoneyFormatter,
): Promise<ValuationResults | undefined> {
  return progress(
    repricingTitle(account.name),
    work,
    ({ results }) => singleRevaluationResults(results, account.name, account.currency, format),
    revaluationFailureTitle(account.name),
  )
}

/**
 * "Re-price value" and "Get an estimate": looks the asset up now and says what
 * came of it. `pending` is true while any re-price covering this asset runs,
 * wherever it was started.
 */
export function useRepriceAccount(account: AccountWithBalances) {
  const progress = useProgressToast()
  const moneyText = useMoneyText()
  const sources = useValuationSources()
  const revalue = useRevalueAsset()
  const pending = useRepricing(account.id)

  return {
    available: canReprice(account, sources.data?.configured ?? []),
    pending,
    reprice: () => {
      if (pending) return
      void repriceWithToast(progress, account, revalue.mutateAsync(account.id), moneyText)
    },
  }
}
