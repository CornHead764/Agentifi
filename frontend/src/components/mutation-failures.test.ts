import { QueryClient, type MutationMeta } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import type { ToastOptions } from '@/components/ui'

import { toastFailedMutations } from './mutation-failures'

async function run(client: QueryClient, meta: MutationMeta | undefined, outcome: 'fail' | 'pass') {
  const mutation = client.getMutationCache().build(client, {
    mutationFn: () =>
      outcome === 'fail' ? Promise.reject(new Error('The bank refused it.')) : Promise.resolve(1),
    meta,
  })
  await mutation.execute(undefined).catch(() => undefined)
}

function watched() {
  const client = new QueryClient()
  const shown: ToastOptions[] = []
  const stop = toastFailedMutations(client, (toast) => shown.push(toast))
  return { client, shown, stop }
}

describe('toastFailedMutations', () => {
  it('shows a failed mutation under its own title, with the error as the description', async () => {
    const { client, shown } = watched()
    await run(client, { failure: 'That goal was not saved' }, 'fail')
    expect(shown).toEqual([
      { title: 'That goal was not saved', description: 'The bank refused it.', tone: 'error' },
    ])
  })

  it('falls back to a general title when the mutation names none', async () => {
    const { client, shown } = watched()
    await run(client, undefined, 'fail')
    expect(shown.map((toast) => toast.title)).toEqual(['That change did not save'])
  })

  it('stays quiet for a mutation whose caller shows the failure itself', async () => {
    const { client, shown } = watched()
    await run(client, { failure: false }, 'fail')
    expect(shown).toEqual([])
  })

  it('stays quiet when the mutation succeeds', async () => {
    const { client, shown } = watched()
    await run(client, { failure: 'That goal was not saved' }, 'pass')
    expect(shown).toEqual([])
  })

  it('stops once unsubscribed', async () => {
    const { client, shown, stop } = watched()
    stop()
    await run(client, undefined, 'fail')
    expect(shown).toEqual([])
  })
})
