import { useMemo, useReducer, useRef, type ReactNode } from 'react'

import { signInReducer } from './signInTasks'
import { SignInTasksContext, type SignInTasksValue } from './signInTasks-context'

/**
 * The sign-ins under way, held by the shell so they outlive the page that
 * started them. `SignInHost` draws them.
 */
export function SignInTasksProvider({ children }: { children: ReactNode }) {
  const [tasks, dispatch] = useReducer(signInReducer, [])
  const next = useRef(0)
  // Stable, so a dialog reporting its phase from an effect is not re-run by
  // every other dialog's report.
  const actions = useMemo<Omit<SignInTasksValue, 'tasks'>>(
    () => ({
      open: (target) => {
        next.current += 1
        dispatch({ type: 'open', id: `sign-in-${next.current}`, target })
      },
      minimize: (id) => dispatch({ type: 'minimize', id }),
      restore: (id) => dispatch({ type: 'restore', id }),
      report: (id, phase, note) => dispatch({ type: 'phase', id, phase, note }),
      dismiss: (id) => dispatch({ type: 'dismiss', id }),
    }),
    [],
  )
  const value = useMemo<SignInTasksValue>(() => ({ tasks, ...actions }), [tasks, actions])
  return <SignInTasksContext value={value}>{children}</SignInTasksContext>
}
