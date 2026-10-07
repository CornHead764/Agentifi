import { useMemo, useState } from 'react'

import { useCurrentSpace } from '@/lib/clients/spaces'
import { useAccounts } from '@/lib/transactions/queries'

import { AccountsDrawer, type AccountsDrawerProps } from './AccountsDrawer'
import { buildAccountTree } from './accountTree'
import { NewAccountDialog } from './NewAccountDialog'

/**
 * The accounts drawer over `GET /accounts`, mounted only where the drawer is
 * shown. The tree stays undefined until the accounts arrive, which the drawer
 * draws as a skeleton; an empty tree would read as "no accounts".
 *
 * The space's chosen account types narrow it here rather than in the tree
 * builder, which the transactions page also uses to offer every account.
 */
export function ConnectedAccountsDrawer(props: Omit<AccountsDrawerProps, 'tree' | 'error'>) {
  const accounts = useAccounts()
  const { data: space } = useCurrentSpace()
  const types = space?.sidebar_account_types ?? null
  const tree = useMemo(
    () => (accounts.data === undefined ? undefined : buildAccountTree(accounts.data, types)),
    [accounts.data, types],
  )
  // The dialog lives here: the drawer is presentational.
  const [adding, setAdding] = useState(false)

  return (
    <>
      <AccountsDrawer
        {...props}
        tree={tree}
        error={accounts.error}
        onNewAccount={props.onNewAccount ?? (() => setAdding(true))}
      />
      <NewAccountDialog open={adding} onOpenChange={setAdding} />
    </>
  )
}
