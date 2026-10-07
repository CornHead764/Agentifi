import { LogIn, RefreshCw } from 'lucide-react'

import { Badge, Button, Spinner } from '@/components/ui'

import type { SignInNeed } from './signInNeed'

/**
 * A login's state in a word. A waiting code request outranks everything else:
 * it is the only state where the household must act before the next run can
 * work. `idle` is the word for a login that never signed in.
 */
export function ConnectorBadge({
  need,
  needsSignIn,
  failed,
  connected,
  waiting = false,
  idle,
}: {
  need: SignInNeed
  needsSignIn: boolean
  /** The last run failed for a reason other than signing in. */
  failed: boolean
  connected: boolean
  /** A code request is waiting for an answer. */
  waiting?: boolean
  idle: string
}) {
  if (waiting) return <Badge tone="warning">Code needed</Badge>
  if (need === 'password-signs-in') return <Badge>Session expired</Badge>
  if (need === 'password-refused') return <Badge tone="warning">Password refused</Badge>
  if (need === 'code-needed') return <Badge tone="warning">Code needed</Badge>
  if (needsSignIn) return <Badge tone="warning">Needs a sign-in</Badge>
  if (failed) return <Badge tone="warning">Update failed</Badge>
  if (connected) return <Badge tone="accent">Connected</Badge>
  return <Badge>{idle}</Badge>
}

/** A connector row's sign-in button; `label` comes from `signInLabel`. */
export function SignInButton({
  label,
  disabled = false,
  title,
  onClick,
}: {
  label: string
  disabled?: boolean
  /** Why it is disabled, as a hover hint. */
  title?: string
  onClick: () => void
}) {
  return (
    <Button size="sm" variant="primary" disabled={disabled} title={title} onClick={onClick}>
      <LogIn size={14} aria-hidden="true" /> {label}
    </Button>
  )
}

/**
 * Run a connector now: update a login, read a mailbox, sync a bank. `busy`
 * is this run; `disabled` is anything else that holds it back.
 */
export function RefreshButton({
  busy,
  label,
  busyLabel,
  disabled = false,
  title,
  onClick,
}: {
  busy: boolean
  label: string
  busyLabel: string
  disabled?: boolean
  title?: string
  onClick: () => void
}) {
  return (
    <Button
      size="sm"
      variant="secondary"
      disabled={busy || disabled}
      title={title}
      onClick={onClick}
    >
      {busy ? <Spinner /> : <RefreshCw size={14} aria-hidden="true" />} {busy ? busyLabel : label}
    </Button>
  )
}

/** Said under a login whose session lapsed while a password is kept. */
export function SessionExpiredNote() {
  return (
    <p className="connector__reason muted">
      Session expired; the kept password signs in at the next update.
    </p>
  )
}
