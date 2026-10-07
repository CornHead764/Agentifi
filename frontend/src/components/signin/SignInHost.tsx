import { lazy, Suspense, useContext, useEffect, useRef } from 'react'

import { useToastOrNull } from '@/components/ui'

import { clearsAfter, type SignInTask, type SignInViewProps } from './signInTasks'
import { SignInTasksContext, type SignInTasksValue } from './signInTasks-context'
import { useSignInToasts } from './SignInToasts'

// Loaded when the first sign-in opens rather than with the shell: most visits
// to the application never start one.
const ConnectDialog = lazy(() =>
  import('@/pages/settings/bills/ConnectDialog').then((module) => ({
    default: module.ConnectDialog,
  })),
)
const SignInDialog = lazy(() =>
  import('@/pages/settings/merchant/SignInDialog').then((module) => ({
    default: module.SignInDialog,
  })),
)
const MailboxSignInDialog = lazy(() =>
  import('@/pages/settings/email/MailboxSignInDialog').then((module) => ({
    default: module.MailboxSignInDialog,
  })),
)

/**
 * Every sign-in under way, as its dialog: mounted whether or not it is on
 * screen, so its poll and its form carry on while it is a toast. Mounted once,
 * in the shell, so a sign-in outlives the page it was started from.
 */
export function SignInDialogs() {
  const signIns = useContext(SignInTasksContext)
  if (signIns === null || signIns.tasks.length === 0) return null
  return <HostedDialogs signIns={signIns} />
}

/** A toast for each sign-in put away. */
export function SignInToasts() {
  const signIns = useContext(SignInTasksContext)
  useSignInToasts(useToastOrNull(), signIns?.tasks ?? NONE, signIns?.restore ?? ignore)
  return null
}

const NONE: readonly SignInTask[] = []
const ignore = () => {}

function HostedDialogs({ signIns }: { signIns: SignInTasksValue }) {
  useClearingPills(signIns.tasks, signIns.dismiss)
  return (
    <Suspense fallback={null}>
      {signIns.tasks.map((task) => (
        <HostedDialog key={task.id} task={task} signIns={signIns} />
      ))}
    </Suspense>
  )
}

function HostedDialog({
  task,
  signIns,
}: {
  task: SignInTask
  signIns: SignInTasksValue
}) {
  const view: SignInViewProps = {
    minimized: task.minimized,
    onMinimize: () => signIns.minimize(task.id),
    onPhase: (phase, note) => signIns.report(task.id, phase, note),
    onClose: () => signIns.dismiss(task.id),
  }
  const { target } = task
  switch (target.kind) {
    case 'bill':
      return (
        <ConnectDialog
          connection={target.connection}
          provider={target.provider}
          {...view}
        />
      )
    case 'merchant':
      return (
        <SignInDialog
          merchant={target.merchant}
          account={target.account}
          {...view}
        />
      )
    case 'mailbox':
      return <MailboxSignInDialog connection={target.connection} {...view} />
  }
}

/**
 * A toast whose sign-in went well leaves on its own after a moment. Its timer
 * is its own: another toast changing does not restart it.
 */
function useClearingPills(tasks: readonly SignInTask[], dismiss: (id: string) => void) {
  const timers = useRef(new Map<string, ReturnType<typeof setTimeout>>())
  useEffect(() => {
    const running = timers.current
    const clearing = new Set<string>()
    for (const task of tasks) {
      const after = clearsAfter(task)
      if (after === null) continue
      clearing.add(task.id)
      if (!running.has(task.id)) running.set(task.id, setTimeout(() => dismiss(task.id), after))
    }
    for (const [id, timer] of running) {
      if (clearing.has(id)) continue
      clearTimeout(timer)
      running.delete(id)
    }
  }, [tasks, dismiss])
  useEffect(() => {
    const running = timers.current
    return () => {
      for (const timer of running.values()) clearTimeout(timer)
      running.clear()
    }
  }, [])
}
