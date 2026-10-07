import { clsx } from 'clsx'
import { Tabs as Radix } from 'radix-ui'
import { useRef, type ComponentProps } from 'react'

import { useBrowserLayoutEffect, useOverflowEdges } from './overflow-edges'

export const Tabs = Radix.Root

/**
 * The tab strip, which on a phone is wider than the phone. It fades on
 * whichever side still has tabs, and scrolls the selected tab into view, since
 * a route change or a reload can select one off the end.
 */
export function TabsList({ className, ...props }: ComponentProps<typeof Radix.List>) {
  const list = useRef<HTMLDivElement>(null)
  useOverflowEdges(list)

  useBrowserLayoutEffect(() => {
    const element = list.current
    const active = element?.querySelector<HTMLElement>('[data-state="active"]')
    if (!element || !active) return
    const margin = 16
    const start = active.offsetLeft
    const end = start + active.offsetWidth
    if (start < element.scrollLeft) element.scrollLeft = Math.max(0, start - margin)
    else if (end > element.scrollLeft + element.clientWidth) {
      element.scrollLeft = end - element.clientWidth + margin
    }
  })

  return <Radix.List ref={list} className={clsx('tabs__list', className)} {...props} />
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof Radix.Trigger>) {
  return <Radix.Trigger className={clsx('tabs__trigger', className)} {...props} />
}

export function TabsContent({ className, ...props }: ComponentProps<typeof Radix.Content>) {
  return <Radix.Content className={clsx('tabs__content', className)} {...props} />
}
