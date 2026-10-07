import { useEffect, useRef } from 'react'

import type { ToastId, ToastOptions, ToastTone, ToastValue } from '@/components/ui'

import { pillOf, trayOf, type PillTone, type SignInTask } from './signInTasks'

const TONES: Record<PillTone, ToastTone> = {
  working: 'loading',
  attention: 'neutral',
  success: 'success',
  failure: 'error',
}

/**
 * The toast a minimized sign-in shows. It stays until the sign-in is brought
 * back or cleared: the task decides when it goes, not the toast's timer.
 */
export function toastOf(task: SignInTask, onRestore: (id: string) => void): ToastOptions {
  const pill = pillOf(task)
  return {
    title: pill.text,
    tone: pill.spinning ? 'loading' : TONES[pill.tone],
    duration: Infinity,
    action: { label: 'Show', onSelect: () => onRestore(task.id) },
  }
}

/** The toast shown for each minimized sign-in, by task id. */
export type ShownToasts = Map<string, { id: ToastId; said: string }>

/**
 * One toast per minimized sign-in, replaced when what it says changes and
 * dismissed when its sign-in is restored or cleared. A toast closed by hand
 * comes back with the sign-in's next step.
 */
export function syncSignInToasts(
  toast: ToastValue,
  shown: ShownToasts,
  tasks: readonly SignInTask[],
  onRestore: (id: string) => void,
) {
  const minimized = new Set<string>()
  for (const task of trayOf(tasks)) {
    minimized.add(task.id)
    const options = toastOf(task, onRestore)
    const said = `${options.tone}:${pillOf(task).text}`
    const current = shown.get(task.id)
    if (current === undefined) {
      shown.set(task.id, { id: toast.show(options), said })
    } else if (current.said !== said) {
      toast.update(current.id, options)
      shown.set(task.id, { id: current.id, said })
    }
  }
  for (const [taskId, current] of shown) {
    if (minimized.has(taskId)) continue
    toast.dismiss(current.id)
    shown.delete(taskId)
  }
}

export function useSignInToasts(
  toast: ToastValue | null,
  tasks: readonly SignInTask[],
  onRestore: (id: string) => void,
) {
  const shown = useRef<ShownToasts>(new Map())
  useEffect(() => {
    if (toast !== null) syncSignInToasts(toast, shown.current, tasks, onRestore)
  }, [toast, tasks, onRestore])
  useEffect(() => {
    const showing = shown.current
    return () => {
      if (toast !== null) for (const current of showing.values()) toast.dismiss(current.id)
      showing.clear()
    }
  }, [toast])
}
