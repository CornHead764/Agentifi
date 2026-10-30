import { createContext, useContext } from 'react'

import type { SignInPhase, SignInTarget, SignInTask } from './signInTasks'

export interface SignInTasksValue {
  tasks: readonly SignInTask[]
  /** Start a sign-in, or bring back the one already under way for that login. */
  open: (target: SignInTarget) => void
  minimize: (id: string) => void
  restore: (id: string) => void
  report: (id: string, phase: SignInPhase, note: string) => void
  dismiss: (id: string) => void
}

export const SignInTasksContext = createContext<SignInTasksValue | null>(null)

/**
 * A page rendered on its own — a test rendering one settings section to a
 * string — has no shell above it and so nothing to open a sign-in in. It gets
 * a value that opens nothing rather than an exception out of its first render.
 */
const NO_SHELL: SignInTasksValue = {
  tasks: [],
  open: () => {},
  minimize: () => {},
  restore: () => {},
  report: () => {},
  dismiss: () => {},
}

export function useSignIns(): SignInTasksValue {
  return useContext(SignInTasksContext) ?? NO_SHELL
}
