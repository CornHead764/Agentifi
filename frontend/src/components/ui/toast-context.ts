import { createContext, useContext, type ReactNode } from 'react'

/** `loading` is work still running: it spins, and stays until updated or dismissed. */
export type ToastTone = 'neutral' | 'success' | 'error' | 'loading'

export interface ToastOptions {
  title: ReactNode
  description?: ReactNode
  tone?: ToastTone
  /**
   * Milliseconds. Left out, the person's own toast duration; an error or a
   * loading toast stays until dismissed unless told otherwise.
   */
  duration?: number
  /**
   * One button that does the next thing — opens what was just made, or the
   * place that explains a failure. The toast sits outside the router, so the
   * caller navigates in `onSelect` rather than passing a link.
   */
  action?: { label: string; onSelect: () => void }
}

export type ToastId = number

export interface ToastValue {
  show: (toast: ToastOptions) => ToastId
  /** Replaces a toast where it stands, or shows it anew if that one was dismissed. */
  update: (id: ToastId, toast: ToastOptions) => void
  dismiss: (id: ToastId) => void
}

export interface QueuedToast extends ToastOptions {
  id: ToastId
  /** Bumped by an update, so the replacement mounts afresh and is announced. */
  version: number
}

/** The queue with one toast replaced where it stands, or added if it was dismissed. */
export function replaceToast(
  queue: readonly QueuedToast[],
  id: ToastId,
  toast: ToastOptions,
): QueuedToast[] {
  const index = queue.findIndex((one) => one.id === id)
  if (index === -1) return [...queue, { ...toast, id, version: 0 }]
  const next = [...queue]
  next[index] = { ...toast, id, version: queue[index].version + 1 }
  return next
}

/**
 * How long a toast stays. An error that vanishes on its own is an error the
 * user never read, so it waits for a dismissal; loading waits for its result.
 */
export function toastDuration(toast: ToastOptions, preferredMs: number): number {
  if (toast.duration !== undefined) return toast.duration
  return toast.tone === 'error' || toast.tone === 'loading' ? Infinity : preferredMs
}

export const ToastContext = createContext<ToastValue | null>(null)

export function useToast(): ToastValue {
  const value = useContext(ToastContext)
  if (!value) throw new Error('useToast must be used inside <ToastProvider>')
  return value
}

/**
 * The toast, or nothing, for a hook that is also rendered by the test harness
 * and `react-dom/server`: a missing toast is never worth an exception.
 */
export function useToastOrNull(): ToastValue | null {
  return useContext(ToastContext)
}
