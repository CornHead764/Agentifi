import { useCallback, useState } from 'react'

import { useHeld } from './opening-key'

/** The part of a mutation a confirmation drives. */
export interface Confirmable<V, R = unknown> {
  mutate(variables: V, callbacks: { onSuccess: (result: R) => void }): void
  isPending: boolean
}

export interface ConfirmOptions<T, V, R> {
  /** What the mutation is called with, when that is not the target itself. */
  variables?: (target: T) => V
  /** Runs when the mutation succeeds, as the dialog closes. */
  onSuccess?: (result: R, target: T) => void
}

export interface Confirm<T> {
  /** Open the confirmation on a target. Stable across renders. */
  ask: (target: T) => void
  /** The target asked about, still there while the dialog animates closed. */
  target: T | undefined
  /** Spread onto `ConfirmDialog`. */
  dialog: { open: boolean; pending: boolean; onConfirm: () => void; onCancel: () => void }
}

/**
 * Run a confirmed mutation: the dialog closes when it succeeds and stays open
 * on a failure, which the mutation's own toast reports, so the user can try
 * again or cancel.
 */
export function runConfirmed<V, R>(
  mutation: Confirmable<V, R>,
  variables: V,
  close: (result: R) => void,
) {
  mutation.mutate(variables, { onSuccess: close })
}

/**
 * A `ConfirmDialog` over a mutation. It stays open while the mutation runs,
 * closes when it succeeds, and stays open with the failure toast when it does
 * not.
 */
export function useConfirm<T, V, R = unknown>(
  mutation: Confirmable<V, R>,
  options: ConfirmOptions<T, V, R> & { variables: (target: T) => V },
): Confirm<T>
export function useConfirm<T, R = unknown>(
  mutation: Confirmable<T, R>,
  options?: Omit<ConfirmOptions<T, T, R>, 'variables'>,
): Confirm<T>
export function useConfirm<T, V, R>(
  mutation: Confirmable<V, R>,
  options: ConfirmOptions<T, V, R> = {},
): Confirm<T> {
  const [asked, setAsked] = useState<{ target: T } | null>(null)
  const held = useHeld(asked)
  const run: Confirmable<T | V, R> = mutation
  const pending = asked !== null && mutation.isPending
  const ask = useCallback((target: T) => setAsked({ target }), [])
  return {
    ask,
    target: held?.target,
    dialog: {
      open: asked !== null,
      pending,
      onConfirm: () => {
        if (asked === null) return
        const { variables, onSuccess } = options
        runConfirmed(
          run,
          variables ? variables(asked.target) : asked.target,
          (result) => {
            setAsked(null)
            onSuccess?.(result, asked.target)
          },
        )
      },
      onCancel: () => {
        if (!pending) setAsked(null)
      },
    },
  }
}
