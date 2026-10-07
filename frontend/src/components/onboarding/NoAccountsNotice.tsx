import { useState } from 'react'
import { Link, useLocation } from 'react-router-dom'

import { NewAccountDialog } from '@/components/shell/NewAccountDialog'
import { Button, Card } from '@/components/ui'
import { useAccounts } from '@/lib/transactions/queries'

import { CONNECT_PATH, IMPORT_PATH } from './paths'

/**
 * Said above every page while the space has no accounts, so $0 figures read
 * as "nothing here yet". The dashboard, the register and settings say it in
 * their own way instead.
 */
export function NoAccountsNotice() {
  const { pathname } = useLocation()
  const accounts = useAccounts()
  const [addingByHand, setAddingByHand] = useState(false)

  if (pathname === '/' || pathname === '/transactions' || pathname.startsWith('/settings')) {
    return null
  }
  if (!accounts.isSuccess || accounts.data.length > 0) return null

  return (
    <>
      <Card
        className="no-accounts"
        title="No accounts yet"
        subtitle="Figures stay at zero until an account is added."
        actions={
          <>
            <Button asChild variant="primary" size="sm">
              <Link to={CONNECT_PATH}>Connect with SimpleFIN</Link>
            </Button>
            <Button asChild size="sm">
              <Link to={IMPORT_PATH}>Import a file</Link>
            </Button>
            <Button size="sm" onClick={() => setAddingByHand(true)}>
              Add by hand
            </Button>
          </>
        }
      />
      <NewAccountDialog open={addingByHand} onOpenChange={setAddingByHand} />
    </>
  )
}
