import { useMerchantAccounts, useMerchantAgent } from '@/lib/clients/merchant'
import { MERCHANTS, type MerchantId } from '@/lib/merchants'

import { AccountsCard } from './merchant/AccountsCard'
import { EbaySetupCard } from './merchant/EbaySetupCard'
import { ImportCard } from './merchant/ImportCard'
import { OrdersCard } from './merchant/OrdersCard'

/**
 * A merchant connector screen: the household's logins at the shop, a file
 * import where the shop sells its history as files, and what is on file with
 * how many bank rows have been matched to it.
 */
export function MerchantSettings({ merchant }: { merchant: MerchantId }) {
  const accounts = useMerchantAccounts(merchant)
  const agent = useMerchantAgent(merchant)

  return (
    <div className="stack">
      {merchant === 'ebay' ? <EbaySetupCard needsKeys={agent.data?.needs_keys ?? false} /> : null}
      <AccountsCard
        merchant={merchant}
        accounts={accounts.data ?? []}
        loading={accounts.isPending}
        agent={agent.data}
      />
      {MERCHANTS[merchant].files.length > 0 ? (
        <ImportCard merchant={merchant} accounts={accounts.data ?? []} />
      ) : null}
      <OrdersCard merchant={merchant} accounts={accounts.data ?? []} />
    </div>
  )
}
