import type { MouseEvent } from 'react'

/**
 * Everything that answers a click itself keeps it. Radix renders its triggers
 * as buttons, so the element selector covers the pickers too.
 */
export const ROW_CONTROLS =
  'a, button, input, select, textarea, label, summary, ' +
  '[role="button"], [role="checkbox"], [role="switch"], [role="menuitem"], [role="tab"], ' +
  '[data-row-click-ignore]'

/** The slice of a DOM element the rule reads, so it can be tested without a DOM. */
export interface ClickNode {
  closest(selector: string): unknown
}

export interface RowClick {
  row: { contains(node: unknown): boolean }
  target: unknown
  /** Whether the person has text selected: a drag across a name ends in a click. */
  selecting: boolean
}

/**
 * Whether a click on a row means "open it". The press must have landed in the
 * row's own markup: React bubbles a portal's synthetic events up the component
 * tree, so a click on a menu item or a dialog the row opened arrives here as a
 * row click.
 */
export function opensRow({ row, target, selecting }: RowClick): boolean {
  if (selecting) return false
  if (!isClickNode(target) || !row.contains(target)) return false
  return target.closest(ROW_CONTROLS) === null
}

function isClickNode(target: unknown): target is ClickNode {
  return typeof target === 'object' && target !== null && 'closest' in target && typeof target.closest === 'function'
}

/**
 * The props that make a whole row open `open` on a pointer click. The row's own
 * button or link stays the keyboard and screen-reader way in.
 */
export function rowOpenProps(open: (() => void) | undefined): {
  onClick?: (event: MouseEvent<HTMLElement>) => void
  'data-row-open'?: ''
} {
  if (open === undefined) return {}
  return {
    'data-row-open': '',
    onClick: (event) => {
      const selection = typeof window === 'undefined' ? null : window.getSelection()
      const selecting = selection !== null && !selection.isCollapsed
      if (opensRow({ row: event.currentTarget, target: event.target, selecting })) open()
    },
  }
}
