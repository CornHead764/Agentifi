import { useQueryClient } from '@tanstack/react-query'
import { ExternalLink } from 'lucide-react'
import { useEffect, useRef, useState, useEffectEvent } from 'react'

import { ConnectorFailureNote } from '@/components/ConnectorFailureNote'
import {
  canMinimize,
  leaving,
  type SignInPhase,
  type SignInViewProps,
} from '@/components/signin/signInTasks'
import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  Input,
  failureToast,
  useToast,
} from '@/components/ui'
import { ApiError } from '@/lib/api'
import {
  useMailboxSignInStatus,
  useSignInMailboxWithPassword,
  useStartMailboxDeviceSignIn,
  type EmailConnection,
  type MailboxDeviceCode,
  EMAIL_KEY,
} from '@/lib/clients/email'
import { BILLS_KEY } from '@/lib/clients/bills'

import { useReportPhase } from '../connector/signInFlow'

/**
 * Proving Agentifi may read the mailbox. Office 365 refuses passwords, so it
 * signs in by device code and the refresh token never passes through this
 * page. IMAP takes an app password, sealed on the server and cleared from this
 * component as soon as there is an answer. Nothing that comes back is a secret.
 *
 * Hosted by the shell (`SignInHost`), so the device-code wait can be put away
 * as a pill.
 */
export function MailboxSignInDialog({
  connection,
  minimized,
  onMinimize,
  onPhase,
  onClose,
}: SignInViewProps & {
  connection: EmailConnection
}) {
  // Neither path has anything on the server to give up: a device code runs out
  // by itself, and an app password is one request.
  const [phase, setPhase] = useState<SignInPhase>('form')
  const leave = (how: 'close' | 'cancel') => {
    if (leaving(phase, how).then === 'minimize') onMinimize()
    else onClose()
  }
  const report = (next: SignInPhase, note: string) => {
    setPhase(next)
    onPhase(next, note)
  }
  const cancel = () => leave('cancel')
  return (
    <Dialog open={!minimized} onOpenChange={(open) => (open ? undefined : leave('close'))}>
      {connection.kind === 'graph' ? (
        <DeviceCode
          connection={connection}
          onClose={onClose}
          onPhase={report}
          onCancel={canMinimize(phase) ? cancel : null}
        />
      ) : (
        <AppPassword
          connection={connection}
          onClose={onClose}
          onPhase={report}
          onCancel={cancel}
        />
      )}
    </Dialog>
  )
}

/**
 * The Office 365 path. The sign-in starts as the dialog opens because the code
 * expires on its own timer. The guard keeps it to one start: React runs an
 * effect twice in development, which would poll a second session under the
 * first one's code.
 */
function DeviceCode({
  connection,
  onClose,
  onPhase,
  onCancel,
}: {
  connection: EmailConnection
  onClose: () => void
  onPhase: (phase: SignInPhase, note: string) => void
  /** Null while there is nothing running to give up; Close is enough then. */
  onCancel: (() => void) | null
}) {
  const { show } = useToast()
  const client = useQueryClient()
  const start = useStartMailboxDeviceSignIn()
  const [code, setCode] = useState<MailboxDeviceCode | null>(null)
  const started = useRef(false)

  const begin = () => {
    if (start.isPending) return
    setCode(null)
    start.mutate(connection.id, { onSuccess: setCode })
  }

  const beginOnMount = useEffectEvent(begin)
  useEffect(() => {
    if (started.current) return
    started.current = true
    beginOnMount()
  }, [])

  const status = useMailboxSignInStatus(connection.id, code?.session_id ?? null)
  const state = status.data?.state ?? 'pending'

  // Landing is the one step nobody presses a button for: Microsoft accepts the
  // code in the other browser and the poll is what hears it.
  const landed = useRef(false)
  const land = useEffectEvent(() => {
    void client.invalidateQueries({ queryKey: EMAIL_KEY })
    void client.invalidateQueries({ queryKey: BILLS_KEY })
    show({ title: `Agentifi can read ${connection.address}` })
    onClose()
  })
  useEffect(() => {
    if (state !== 'signed_in' || landed.current) return
    landed.current = true
    land()
  }, [state])

  const error = status.data?.error ?? ''
  const refused =
    state === 'failed' || state === 'expired'
      ? error !== ''
        ? error
        : state === 'expired'
          ? 'The code expired before it was entered. Start again for a fresh one.'
          : 'Microsoft refused the sign-in.'
      : null

  const phase: SignInPhase =
    refused !== null ? 'failed' : code === null ? 'working' : 'approval'
  const note =
    phase === 'approval' && code !== null
      ? `${connection.label}: enter ${code.user_code} at Microsoft`
      : phase === 'failed'
        ? `${connection.label}: ${refused}`
        : ''
  useReportPhase(phase, note, onPhase)

  return (
    <DialogContent
      title={`Sign in to ${connection.label}`}
      description="Enter this code at Microsoft in your own browser. Agentifi never sees the password."
      footer={
        <DialogActions
          cancel="Close"
          start={
            onCancel === null ? null : (
              <Button type="button" variant="ghost" onClick={onCancel}>
                Cancel sign-in
              </Button>
            )
          }
        >
          {refused === null ? null : (
            <Button type="button" variant="primary" disabled={start.isPending} onClick={begin}>
              Start again
            </Button>
          )}
        </DialogActions>
      }
    >
      {refused !== null ? <ConnectorFailureNote raw={refused} provider="Microsoft" /> : null}

      {code === null ? (
        <p className="muted">{start.isPending ? 'Asking Microsoft for a code…' : 'No code yet.'}</p>
      ) : (
        <>
          <p className="mailbox-code" aria-label={`The code to enter is ${code.user_code}`}>
            {code.user_code}
          </p>
          <p>
            Open{' '}
            <a href={code.verification_uri} target="_blank" rel="noreferrer">
              {code.verification_uri} <ExternalLink size={12} aria-hidden="true" />
            </a>{' '}
            in another tab, enter the code, and sign in as an account that can read{' '}
            {connection.address}.
          </p>
          <p className="muted">
            {refused === null
              ? 'This box notices on its own once Microsoft accepts it; leave it open.'
              : ''}
          </p>
        </>
      )}
    </DialogContent>
  )
}

/**
 * The IMAP path. The password lives in state only while the request is in the
 * air and is cleared on any answer. The mailbox's own refusal is shown: Gmail's
 * "Invalid credentials (Failure)" means an account password was used where an
 * app password was wanted.
 */
function AppPassword({
  connection,
  onClose,
  onPhase,
  onCancel,
}: {
  connection: EmailConnection
  onClose: () => void
  onPhase: (phase: SignInPhase, note: string) => void
  onCancel: () => void
}) {
  const { show } = useToast()
  const [password, setPassword] = useState('')
  const [refused, setRefused] = useState<string | null>(null)
  const signIn = useSignInMailboxWithPassword()

  const phase: SignInPhase = signIn.isPending ? 'working' : refused !== null ? 'failed' : 'form'
  useReportPhase(phase, '', onPhase)

  const submit = () => {
    if (password === '' || signIn.isPending) return
    setRefused(null)
    signIn.mutate(
      { id: connection.id, password },
      {
        onSuccess: () => {
          show({ title: `Agentifi can read ${connection.address}` })
          onClose()
        },
        onError: (error: unknown) => {
          if (error instanceof ApiError && error.status === 422) setRefused(refusal(error))
          else show(failureToast(error))
        },
        // `reset` too: a mutation keeps its variables in the query cache until
        // collected, which for a password is a copy nobody asked for.
        onSettled: () => {
          setPassword('')
          signIn.reset()
        },
      },
    )
  }

  return (
    <DialogContent
      title={`Sign in to ${connection.label}`}
      description={`For ${connection.address}. Stored sealed; never shown again.`}
      onSubmit={(event) => {
        event.preventDefault()
        submit()
      }}
      footer={
        <DialogActions onCancel={onCancel}>
          <Button type="submit" variant="primary" disabled={password === '' || signIn.isPending}>
            {signIn.isPending ? 'Checking…' : 'Sign in'}
          </Button>
        </DialogActions>
      }
    >
      {refused === null ? null : (
        <ConnectorFailureNote raw={refused} provider={connection.label} />
      )}

      <Field
        label="App password"
        hint="Not the account password. Gmail: Security → App passwords (needs 2-Step Verification)."
      >
        <Input
          type="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
          autoComplete="off"
          autoFocus
        />
      </Field>
    </DialogContent>
  )
}

/** The mailbox's own words for the refusal, or a sentence when it gave none. */
function refusal(error: ApiError): string {
  return error.detail ?? 'The mailbox refused that password.'
}
