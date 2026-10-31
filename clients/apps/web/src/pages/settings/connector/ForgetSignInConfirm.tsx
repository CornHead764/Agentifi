/** "Forget the sign-in", for every connector that keeps one: a mailbox, a bill provider, a shop login. */

import { ConfirmDialog, type Confirm } from '@/components/ui'

import type { SignInOf } from './useForgetSignIn'

export function ForgetSignInConfirm({
  confirm,
  kept,
}: {
  confirm: Confirm<SignInOf>
  /** What stays on file, as the sentence's subject: "Bills on file". */
  kept: string
}) {
  return (
    <ConfirmDialog
      {...confirm.dialog}
      title={`Forget the sign-in for ${confirm.target?.name ?? 'this connection'}?`}
      description={`Nothing is fetched until someone signs in again. ${kept} stay.`}
      confirmLabel="Forget sign-in"
    />
  )
}
