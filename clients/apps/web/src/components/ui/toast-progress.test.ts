import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { failureToast } from './failure-toast'
import { replaceToast, toastDuration, type QueuedToast, type ToastOptions } from './toast-context'
import { useProgressToast, withProgressToast, type RunWithProgress } from './toast-progress'

/** A toast that records what it was asked to show, as the provider would lay it out. */
function recorder() {
  let queue: QueuedToast[] = []
  let next = 0
  return {
    get queue() {
      return queue
    },
    show(toast: ToastOptions) {
      next += 1
      queue = [...queue, { ...toast, id: next, version: 0 }]
      return next
    },
    update(id: number, toast: ToastOptions) {
      queue = replaceToast(queue, id, toast)
    },
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((yes, no) => {
    resolve = yes
    reject = no
  })
  return { promise, resolve, reject }
}

describe('a progress toast', () => {
  it('spins while the work runs, then becomes its result in the same place', async () => {
    const toast = recorder()
    toast.show({ title: 'Earlier' })
    const work = deferred<number>()
    const running = withProgressToast(
      toast,
      'Re-pricing Blue Roadster…',
      work.promise,
      (value) => ({ title: `Got ${value}`, tone: 'success' }),
      () => ({ title: 'Failed', tone: 'error' }),
    )
    expect(toast.queue.map((one) => [one.title, one.tone])).toEqual([
      ['Earlier', undefined],
      ['Re-pricing Blue Roadster…', 'loading'],
    ])

    work.resolve(7)
    expect(await running).toBe(7)
    expect(toast.queue).toHaveLength(2)
    expect(toast.queue[1]).toMatchObject({ id: 2, version: 1, title: 'Got 7', tone: 'success' })
  })

  it('becomes the failure, once, and does not reject', async () => {
    const toast = recorder()
    const running = withProgressToast(
      toast,
      'Re-pricing every asset…',
      Promise.reject(new Error('The server is busy')),
      () => ({ title: 'Done' }),
      (error) => ({ title: 'Not re-priced', description: String(error), tone: 'error' }),
    )
    expect(await running).toBeUndefined()
    expect(toast.queue).toEqual([
      {
        id: 1,
        version: 1,
        title: 'Not re-priced',
        description: 'Error: The server is busy',
        tone: 'error',
      },
    ])
  })

  it('says the server\'s sentence under a default title when given no failure', async () => {
    const toast = recorder()
    await withProgressToast(
      toast,
      'Syncing Example Bank…',
      Promise.reject(new Error('The bank is not answering')),
      { title: 'Synced' },
    )
    expect(toast.queue).toEqual([
      {
        id: 1,
        version: 1,
        title: 'That did not work',
        description: 'The bank is not answering',
        tone: 'error',
      },
    ])
  })

  it('puts a failure title over the server\'s sentence', async () => {
    const toast = recorder()
    await withProgressToast(
      toast,
      'Syncing Example Bank…',
      Promise.reject(new Error('The bank is not answering')),
      { title: 'Synced' },
      'Example Bank was not synced',
    )
    expect(toast.queue[0]).toMatchObject({
      title: 'Example Bank was not synced',
      description: 'The bank is not answering',
      tone: 'error',
    })
  })

  it('shows a fixed outcome whatever the work returned', async () => {
    const toast = recorder()
    expect(
      await withProgressToast(toast, 'Refreshing prices…', Promise.resolve('ignored'), {
        title: 'Prices refreshed',
        tone: 'success',
      }),
    ).toBe('ignored')
    expect(toast.queue[0]).toMatchObject({ title: 'Prices refreshed', tone: 'success' })
  })

  it('shows the result anew when the loading toast was dismissed', () => {
    const queue = replaceToast([], 4, { title: 'Blue Roadster re-priced', tone: 'success' })
    expect(queue).toEqual([
      { id: 4, version: 0, title: 'Blue Roadster re-priced', tone: 'success' },
    ])
  })
})

describe('how long a toast stays', () => {
  it('is the person\'s own duration for news, and until dismissed for an error or running work', () => {
    expect(toastDuration({ title: 'Saved', tone: 'success' }, 9000)).toBe(9000)
    expect(toastDuration({ title: 'Synced' }, 9000)).toBe(9000)
    expect(toastDuration({ title: 'Not saved', tone: 'error' }, 9000)).toBe(Infinity)
    expect(toastDuration({ title: 'Syncing…', tone: 'loading' }, 9000)).toBe(Infinity)
  })

  it('is the caller\'s when the caller names one', () => {
    expect(toastDuration({ title: 'Copied', tone: 'success', duration: 2000 }, 9000)).toBe(2000)
  })
})

describe('a failure toast', () => {
  it('is a title over the server\'s sentence', () => {
    expect(failureToast(new Error('The name is taken'), 'That change did not save')).toEqual({
      title: 'That change did not save',
      description: 'The name is taken',
      tone: 'error',
    })
    expect(failureToast(new Error('The name is taken')).title).toBe('That did not work')
  })
})

describe('the progress hook', () => {
  it('still runs the work, silently, outside a toast provider', async () => {
    let progress: RunWithProgress | undefined
    function Probe() {
      progress = useProgressToast()
      return null
    }
    renderToStaticMarkup(createElement(Probe))
    expect(await progress!('Working…', Promise.resolve(3), { title: 'Done' })).toBe(3)
    expect(await progress!('Working…', Promise.reject(new Error('No')), { title: 'Done' })).toBe(
      undefined,
    )
  })
})
