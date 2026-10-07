import { useId, type KeyboardEvent } from 'react'

/**
 * Arrow keys for a list of focusable options under a search box. Down from the
 * box enters the list at the top and Up at the bottom; Up from the first
 * option returns to the box; a printable key or Backspace typed in the list
 * goes to the box. Space stays with the option, which it toggles or picks.
 * Focus moves between the real elements, so a list drawn in groups is walked
 * in reading order.
 *
 * Spread `list` on the list's element and `search` on the box; the elements
 * are found by those attributes when a key is pressed.
 */
export function useArrowList(itemSelector: string) {
  const id = useId()

  const listElement = () => document.querySelector(`[data-arrow-list="${id}"]`)
  const searchElement = () =>
    document.querySelector<HTMLInputElement>(`[data-arrow-search="${id}"]`)
  const items = () => [...(listElement()?.querySelectorAll<HTMLElement>(itemSelector) ?? [])]

  const focusAt = (index: number) => {
    const all = items()
    if (all.length === 0) return
    all[index < 0 ? all.length - 1 : index >= all.length ? 0 : index]?.focus()
  }

  /** True when the key moved focus into the list. */
  const onSearchKeyDown = (event: KeyboardEvent<HTMLInputElement>): boolean => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return false
    event.preventDefault()
    focusAt(event.key === 'ArrowDown' ? 0 : -1)
    return true
  }

  const onListKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    const here = items().findIndex((item) => item === document.activeElement)
    const search = searchElement()
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      focusAt(here + 1)
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      if (here <= 0 && search) search.focus()
      else focusAt(here - 1)
      return
    }
    if ((event.key.length === 1 && event.key !== ' ') || event.key === 'Backspace') {
      search?.focus()
    }
  }

  return {
    list: { 'data-arrow-list': id, onKeyDown: onListKeyDown },
    search: { 'data-arrow-search': id },
    items,
    onSearchKeyDown,
  }
}
