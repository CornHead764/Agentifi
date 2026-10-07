import { afterEach, describe, expect, it, vi } from 'vitest'

import { openInNewTab } from './documentActions'

function fakeTab() {
  return { opener: {} as unknown, location: { replace: vi.fn() }, close: vi.fn() }
}

function stubWindow(tab: ReturnType<typeof fakeTab> | null) {
  const open = vi.fn().mockReturnValue(tab)
  vi.stubGlobal('window', { open, setTimeout: vi.fn() })
  const created: Blob[] = []
  vi.stubGlobal('URL', {
    createObjectURL: (blob: Blob) => {
      created.push(blob)
      return 'blob:fixture/1'
    },
    revokeObjectURL: vi.fn(),
  })
  return { open, created }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('opening a stored document', () => {
  it('claims the tab before the bytes arrive, so the click is still the gesture behind it', async () => {
    const tab = fakeTab()
    const { open } = stubWindow(tab)
    let answer: (blob: Blob) => void = () => undefined
    const opened = openInNewTab(() => new Promise((resolve) => (answer = resolve)))

    expect(open).toHaveBeenCalledWith('', '_blank')
    expect(tab.opener).toBeNull()
    expect(tab.location.replace).not.toHaveBeenCalled()

    answer(new Blob(['%PDF-'], { type: 'application/pdf' }))
    await opened
    expect(tab.location.replace).toHaveBeenCalledWith('blob:fixture/1')
  })

  it('shows the file with the type the server sent, so the browser views it rather than saving it', async () => {
    const { created } = stubWindow(fakeTab())
    await openInNewTab(() => Promise.resolve(new Blob(['%PDF-'], { type: 'application/pdf' })))
    expect(created[0]?.type).toBe('application/pdf')
  })

  it('closes the tab it claimed when the file cannot be fetched', async () => {
    const tab = fakeTab()
    stubWindow(tab)
    await expect(openInNewTab(() => Promise.reject(new Error('gone')))).rejects.toThrow('gone')
    expect(tab.close).toHaveBeenCalled()
  })

  it('opens the blob directly when the browser refused the early tab', async () => {
    const { open } = stubWindow(null)
    await openInNewTab(() => Promise.resolve(new Blob(['x'], { type: 'image/png' })))
    expect(open).toHaveBeenLastCalledWith('blob:fixture/1', '_blank', 'noopener')
  })
})
