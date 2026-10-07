import { Plus } from 'lucide-react'
import { useState } from 'react'

import { NewAccountDialog } from '@/components/shell/NewAccountDialog'
import { Button } from '@/components/ui'
import { AddHoldingDialog } from '@/pages/investing/AddHoldingDialog'
import { openInvestmentAccounts } from '@/lib/accounts'
import { useAccounts } from '@/lib/transactions/queries'

/**
 * The next step for an empty portfolio, taken from the dashboard: a holding
 * when there is an investment account to hold it, and otherwise the account.
 * A brokerage that syncs needs neither, and the panel says so in its copy.
 */
export function AddInvestmentAction() {
  const accounts = useAccounts()
  const investment = openInvestmentAccounts(accounts.data ?? [])
  const [adding, setAdding] = useState(false)

  if (!accounts.isSuccess) return null

  if (investment.length === 0) {
    return (
      <>
        <Button size="sm" onClick={() => setAdding(true)}>
          <Plus size={13} aria-hidden="true" /> Add an investment account
        </Button>
        <NewAccountDialog open={adding} onOpenChange={setAdding} initialType="brokerage" />
      </>
    )
  }

  return (
    <>
      <Button size="sm" onClick={() => setAdding(true)}>
        <Plus size={13} aria-hidden="true" /> Add a holding
      </Button>
      <AddHoldingDialog open={adding} onOpenChange={setAdding} accounts={investment} />
    </>
  )
}
