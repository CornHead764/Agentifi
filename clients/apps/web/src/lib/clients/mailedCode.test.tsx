import { QueryClient } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { toastFailedMutations } from '@/components/mutation-failures'
import type { ToastOptions } from '@/components/ui'
import type { Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { useMailedBillCode } from './bills'
import { useMailedMerchantCode } from './merchant'

// With no mailbox the server answers 409; the sign-in dialog beside it already
// has the code field, so nothing is shown for the refusal.
function noMailbox() {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            error: { code: 'conflict', message: 'no mailbox is connected to read the code from' },
          }),
          { status: 409, headers: { 'Content-Type': 'application/json' } },
        ),
      ),
    ),
  )
  const client = new QueryClient()
  const shown: ToastOptions[] = []
  toastFailedMutations(client, (toast) => shown.push(toast))
  return { shown, client }
}

/** The mutation a hook returns, taken from one render under the app's providers. */
function mounted<T>(client: QueryClient, hook: () => T): T {
  let taken: T | undefined
  function Probe() {
    taken = hook()
    return null
  }
  renderScreen(<Probe />, { client })
  return taken as T
}

afterEach(() => vi.unstubAllGlobals())

describe('waiting for a code from the mailbox', () => {
  it('shows nothing when a bill sign-in has no mailbox to read from', async () => {
    const { shown, client } = noMailbox()
    const mutation = mounted(client, useMailedBillCode)
    await expect(mutation.mutateAsync({ connectionId: 'c1' as Uuid, session: 's1' })).rejects.toBeDefined()
    expect(shown).toEqual([])
  })

  it('shows nothing when a shop sign-in has no mailbox to read from', async () => {
    const { shown, client } = noMailbox()
    const mutation = mounted(client, () => useMailedMerchantCode('amazon'))
    await expect(mutation.mutateAsync({ id: 'a1' as Uuid, session: 's1' })).rejects.toBeDefined()
    expect(shown).toEqual([])
  })
})
