import { useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'

import { useToast } from '@/components/ui'

import { toastFailedMutations } from './mutation-failures'

export function MutationFailureToasts() {
  const client = useQueryClient()
  const { show } = useToast()
  useEffect(() => toastFailedMutations(client, show), [client, show])
  return null
}
