import { useCallback } from 'react'

import { useToast, type ToastOptions } from './toast-context'

/**
 * Put `text` on the clipboard. `navigator.clipboard` exists only in a secure
 * context, and a self-hosted deployment is not always served over https, so
 * without it a hidden textarea is selected and copied. The textarea goes inside
 * the open dialog, if any, because a dialog's focus trap pulls focus back out
 * of one appended to the body and the selection goes with it.
 */
export function writeClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) return navigator.clipboard.writeText(text)
  const host = document.activeElement?.closest('[role="dialog"]') ?? document.body
  const returnFocus = document.activeElement
  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '0'
  area.style.left = '0'
  area.style.opacity = '0'
  host.appendChild(area)
  area.select()
  let copied = false
  try {
    copied = document.execCommand('copy')
  } catch {
    copied = false
  } finally {
    area.remove()
    if (returnFocus && 'focus' in returnFocus && typeof returnFocus.focus === 'function') returnFocus.focus()
  }
  return copied ? Promise.resolve() : Promise.reject(new Error('The browser refused to copy.'))
}

/** Put `text` on the clipboard and say whether it went; a refused write asks the reader to copy by hand. */
export function copyWithToast(
  text: string,
  show: (toast: ToastOptions) => unknown,
  copied = 'Copied to clipboard',
  write: (text: string) => Promise<void> = writeClipboard,
): Promise<void> {
  return Promise.resolve()
    .then(() => write(text))
    .then(
      () => void show({ title: copied, tone: 'success', duration: 2000 }),
      () => void show({ title: 'Could not copy', description: 'Select it and copy by hand.', tone: 'error' }),
    )
}

export function useCopy(): (text: string, copied?: string) => void {
  const { show } = useToast()
  return useCallback((text, copied) => void copyWithToast(text, show, copied), [show])
}
