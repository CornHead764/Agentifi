import { useConfirm, useToast, type Confirm, type Confirmable } from '@/components/ui'
import type { Uuid } from '@/lib/transactions/types'

/** The connection whose sign-in is forgotten, named as its row names it. */
export interface SignInOf {
  id: Uuid
  name: string
}

/** Forget the stored sign-in of the connection asked about, and say so once it is gone. */
export function useForgetSignIn(mutation: Confirmable<Uuid>): Confirm<SignInOf> {
  const { show } = useToast()
  return useConfirm(mutation, {
    variables: (connection: SignInOf) => connection.id,
    onSuccess: (_result, connection) =>
      show({ title: `Forgot the sign-in for ${connection.name}` }),
  })
}
