import { describe, expect, it } from 'vitest'

import type { ToastOptions, ToastValue } from '@/components/ui'
import type { BillConnection } from '@/lib/clients/bills'

import type { SignInTask } from './signInTasks'
import { syncSignInToasts, toastOf, type ShownToasts } from './SignInToasts'

const connection = { id: 'conn-power', biller: 'we-energies', label: 'Main account' } as BillConnection

function task(over: Partial<SignInTask>): SignInTask {
  return {
    id: 'a',
    target: { kind: 'bill', connection, provider: null },
    name: 'Example Utility',
    minimized: true,
    phase: 'working',
    note: '',
    ...over,
  }
}

function recorder() {
  const calls: string[] = []
  let next = 0
  const toast: ToastValue = {
    show: (options: ToastOptions) => {
      next += 1
      calls.push(`show ${next} ${options.tone} ${String(options.title)}`)
      return next
    },
    update: (id, options) => calls.push(`update ${id} ${options.tone} ${String(options.title)}`),
    dismiss: (id) => calls.push(`dismiss ${id}`),
  }
  return { toast, calls }
}

describe('a minimized sign-in as a toast', () => {
  it('spins while the server works, and names the step a person has to take', () => {
    expect(toastOf(task({ phase: 'working' }), () => {})).toMatchObject({
      title: 'Example Utility: signing in…',
      tone: 'loading',
      duration: Infinity,
    })
    expect(toastOf(task({ phase: 'code' }), () => {}).tone).toBe('neutral')
    expect(toastOf(task({ phase: 'finished' }), () => {}).tone).toBe('success')
    expect(toastOf(task({ phase: 'stopped', note: 'Last update failed.' }), () => {})).toMatchObject({
      title: 'Last update failed.',
      tone: 'error',
    })
  })

  it('brings its dialog back from the toast’s button', () => {
    const restored: string[] = []
    const options = toastOf(task({ id: 'b' }), (id) => restored.push(id))
    expect(options.action?.label).toBe('Show')
    options.action?.onSelect()
    expect(restored).toEqual(['b'])
  })

  it('shows one toast per minimized sign-in and none for one on screen', () => {
    const { toast, calls } = recorder()
    const shown: ShownToasts = new Map()
    syncSignInToasts(
      toast,
      shown,
      [task({ id: 'a' }), task({ id: 'b', name: 'Example Insurance', phase: 'code' }), task({ id: 'c', minimized: false })],
      () => {},
    )
    expect(calls).toEqual([
      'show 1 loading Example Utility: signing in…',
      'show 2 neutral Example Insurance needs a code',
    ])
  })

  it('replaces a toast only when what it says changes, and dismisses it once restored or cleared', () => {
    const { toast, calls } = recorder()
    const shown: ShownToasts = new Map()
    const restore = () => {}
    syncSignInToasts(toast, shown, [task({ id: 'a' }), task({ id: 'b' })], restore)
    syncSignInToasts(toast, shown, [task({ id: 'a' }), task({ id: 'b' })], restore)
    syncSignInToasts(toast, shown, [task({ id: 'a', phase: 'finished' }), task({ id: 'b', minimized: false })], restore)
    syncSignInToasts(toast, shown, [], restore)
    expect(calls).toEqual([
      'show 1 loading Example Utility: signing in…',
      'show 2 loading Example Utility: signing in…',
      'update 1 success Example Utility: up to date',
      'dismiss 2',
      'dismiss 1',
    ])
    expect(shown.size).toBe(0)
  })
})
