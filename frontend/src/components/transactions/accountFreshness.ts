/**
 * What the account header's freshness badge says. A synced balance and a
 * priced valuation both count as linked, so a Zillow-priced house is not
 * called "Not linked".
 */
import type { BadgeTone } from '@/components/ui'
import { valuationSourceLabel } from '@/lib/accountTypes'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { timeAgo } from '@/lib/format'

export interface FreshnessBadge {
  tone: BadgeTone
  label: string
}

export function accountFreshnessBadge(
  account: Pick<AccountWithBalances, 'provider_balance_at' | 'valuation_source' | 'valued_at'>,
): FreshnessBadge {
  if (account.provider_balance_at !== null) {
    return {
      tone: 'income',
      label: `Updated ${timeAgo(account.provider_balance_at, 'long')}`,
    }
  }
  if (account.valuation_source !== null) {
    const source = valuationSourceLabel(account.valuation_source)
    const priced =
      account.valued_at === null
        ? source
        : `${source} · ${timeAgo(account.valued_at, 'long')}`
    return { tone: 'accent', label: `Priced by ${priced}` }
  }
  return { tone: 'neutral', label: 'Manual' }
}
