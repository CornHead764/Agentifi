import { describe, expect, it } from 'vitest'

import { runConfirmed, type Confirmable } from './useConfirm'

function fakeMutation(outcome: 'success' | 'failure'): Confirmable<string> & { calls: string[] } {
  const calls: string[] = []
  return {
    calls,
    isPending: false,
    mutate(variables, { onSuccess }) {
      calls.push(variables)
      if (outcome === 'success') onSuccess('deleted')
    },
  }
}

describe('runConfirmed', () => {
  it('closes the dialog when the mutation succeeds', () => {
    const mutation = fakeMutation('success')
    const events: string[] = []
    runConfirmed(mutation, 'acct-1', () => events.push('close'))
    expect(mutation.calls).toEqual(['acct-1'])
    expect(events).toEqual(['close'])
  })

  it('stays open when the mutation fails', () => {
    const mutation = fakeMutation('failure')
    const events: string[] = []
    runConfirmed(mutation, 'acct-1', () => events.push('close'))
    expect(mutation.calls).toEqual(['acct-1'])
    expect(events).toEqual([])
  })
})
