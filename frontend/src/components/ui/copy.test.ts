import { afterEach, describe, expect, it, vi } from 'vitest'

import { copyWithToast, writeClipboard } from './copy'
import type { ToastOptions } from './toast-context'

function recorder() {
  const shown: ToastOptions[] = []
  return { shown, show: (toast: ToastOptions) => shown.push(toast) }
}

describe('copyWithToast', () => {
  it('writes the text and confirms with the given title', async () => {
    const written: string[] = []
    const { shown, show } = recorder()
    await copyWithToast('abc', show, 'Recovery codes copied', async (text) => {
      written.push(text)
    })
    expect(written).toEqual(['abc'])
    expect(shown).toEqual([{ title: 'Recovery codes copied', tone: 'success', duration: 2000 }])
  })

  it('asks for a copy by hand when the clipboard refuses', async () => {
    const { shown, show } = recorder()
    await copyWithToast('abc', show, undefined, () => Promise.reject(new Error('denied')))
    expect(shown).toHaveLength(1)
    expect(shown[0].tone).toBe('error')
    expect(shown[0].description).toBe('Select it and copy by hand.')
  })

  it('still tells the reader when the writer throws', async () => {
    const { shown, show } = recorder()
    await copyWithToast('abc', show, undefined, () => {
      throw new TypeError('no clipboard')
    })
    expect(shown[0].tone).toBe('error')
  })
})

/** The few parts of a page the fallback touches, with the text that was selected when it copied. */
function fakePage(copies: boolean) {
  const attached: { value: string; selected: boolean }[] = []
  const copied: string[] = []
  const page = {
    activeElement: null,
    body: {
      appendChild: (area: { value: string; selected: boolean }) => void attached.push(area),
    },
    createElement: () => {
      const area = {
        value: '',
        selected: false,
        style: {},
        setAttribute: () => {},
        select: () => void (area.selected = true),
        remove: () => void attached.splice(attached.indexOf(area), 1),
      }
      return area
    },
    execCommand: (command: string) => {
      if (command === 'copy') copied.push(...attached.filter((a) => a.selected).map((a) => a.value))
      return copies
    },
  }
  return { page, attached, copied }
}

describe('writeClipboard', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('copies through a selected textarea where the page has no clipboard API', async () => {
    const { page, attached, copied } = fakePage(true)
    vi.stubGlobal('navigator', {})
    vi.stubGlobal('document', page)
    await writeClipboard('console.log(1)')
    expect(copied).toEqual(['console.log(1)'])
    expect(attached).toEqual([])
  })

  it('fails when the fallback copy is refused too', async () => {
    vi.stubGlobal('navigator', {})
    vi.stubGlobal('document', fakePage(false).page)
    await expect(writeClipboard('abc')).rejects.toThrow()
  })

  it('uses the clipboard API where there is one', async () => {
    const written: string[] = []
    vi.stubGlobal('navigator', { clipboard: { writeText: async (text: string) => void written.push(text) } })
    await writeClipboard('abc')
    expect(written).toEqual(['abc'])
  })
})
