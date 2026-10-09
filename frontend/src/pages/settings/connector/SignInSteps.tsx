import type { ReactNode } from 'react'

import { Button, Callout, DialogActions, Field, Input, Spinner } from '@/components/ui'
import { useEmailConnections } from '@/lib/clients/email'

import { SecondFactorField } from './SecondFactorField'
import { pagePixels } from './liveView'
import { hasReadableMailbox } from './secondFactor'
import type { AfterSignInState, SignInCredentials } from './signInFlow'

/** The drawing of a provider sign-in's steps, shared by the bill and shop dialogs. */

/** The server is driving the provider's pages and nobody is being asked anything. */
export function SignInWorking({ line }: { line: ReactNode }) {
  return (
    <p className="bill-working" role="status">
      <Spinner size={18} />
      <span>{line}</span>
    </p>
  )
}

/** What the provider is asking for — a code, or the characters in a picture — and the box for it. */
export function SignInCodeStep({
  provider,
  prompt,
  captcha,
  image,
  code,
  onCode,
}: {
  provider: string
  prompt: string
  captcha: boolean
  image: string | null | undefined
  code: string
  onCode: (code: string) => void
}) {
  return (
    <>
      <p>{prompt}</p>
      {captcha && image ? (
        <img
          className="merchant-captcha"
          src={`data:image/png;base64,${image}`}
          alt={`The characters ${provider} is asking you to type`}
        />
      ) : null}
      <Field label={captcha ? 'Characters in the picture' : 'Code'}>
        <Input
          value={code}
          onChange={(event) => onCode(event.target.value)}
          inputMode={captcha ? 'text' : 'numeric'}
          autoComplete="one-time-code"
          autoFocus
        />
      </Field>
    </>
  )
}

/**
 * The provider's page as the server sees it, for a person to act on: a check
 * that says "Verify you are human" is theirs to tick, and the app never ticks
 * it. A click is sent back in the picture's own pixels, which is why the
 * server says how large the page is.
 */
export function SignInLiveView({
  prompt,
  image,
  width,
  height,
  onClick,
}: {
  prompt: string
  image: string | null | undefined
  width: number
  height: number
  onClick: (x: number, y: number) => void
}) {
  return (
    <>
      <p>{prompt}</p>
      {image ? (
        <button
          type="button"
          className="live-view"
          aria-label="The sign-in page. Click on it as you would on the page itself."
          onClick={(event) => {
            const at = pagePixels(
              event.currentTarget.getBoundingClientRect(),
              { width, height },
              { x: event.clientX, y: event.clientY },
            )
            if (at !== null) onClick(at.x, at.y)
          }}
        >
          <img src={`data:image/jpeg;base64,${image}`} alt="" />
        </button>
      ) : (
        <SignInWorking line="Loading the page…" />
      )}
    </>
  )
}

export function SignedInNote() {
  return <p>Signed in. Keeping the session…</p>
}

/** The typed login: username, password and how its second factor is answered. */
export function SignInCredentialsFields({
  provider,
  userLabel,
  credentials,
}: {
  provider: string
  userLabel: string
  credentials: SignInCredentials
}) {
  const mailboxes = useEmailConnections()
  return (
    <>
      <Field label={userLabel}>
        <Input
          value={credentials.username}
          onChange={(event) => credentials.setUsername(event.target.value)}
          autoComplete="username"
        />
      </Field>
      <Field
        label="Password"
        hint="Kept encrypted on this server so updates can sign in on their own, and never shown again."
      >
        <Input
          type="password"
          value={credentials.password}
          onChange={(event) => credentials.setPassword(event.target.value)}
          autoComplete="current-password"
        />
      </Field>
      <SecondFactorField
        provider={provider}
        choice={credentials.factor}
        onChoice={credentials.setFactor}
        authKey={credentials.authKey}
        onAuthKey={credentials.setAuthKey}
        hasMailbox={hasReadableMailbox(mailboxes.data)}
      />
      {credentials.keyProblem !== null ? (
        <Callout tone="warning">{credentials.keyProblem}</Callout>
      ) : null}
    </>
  )
}

/** The first pull after the sign-in: running, and then how it went. */
export function AfterSignIn({
  after,
  fetching,
  summary,
}: {
  after: AfterSignInState
  /** The line while the pull runs. */
  fetching: ReactNode
  summary?: string
}) {
  if (after.phase === 'fetching') return <SignInWorking line={fetching} />
  return (
    <>
      {after.phase === 'stopped' ? <Callout tone="warning">{after.note}</Callout> : <p>{after.note}</p>}
      {summary ? <p className="muted">{summary}</p> : null}
    </>
  )
}

/**
 * Once the session is kept the pull runs on the server, so there is nothing
 * left to cancel: the dialog can only be put away or dismissed.
 */
export function AfterSignInFooter({
  after,
  onClose,
  onDone,
  challengeId = null,
  onAnswer,
}: {
  after: AfterSignInState
  onClose: () => void
  onDone: () => void
  /** The code request the pull stopped on, while it still waits. */
  challengeId?: string | null
  onAnswer?: (challengeId: string) => void
}) {
  if (after.phase === 'fetching')
    return (
      <Button type="button" variant="primary" onClick={onClose}>
        Close
      </Button>
    )
  if (challengeId !== null && onAnswer)
    return (
      <DialogActions cancel="Later" onCancel={onDone}>
        <Button type="button" variant="primary" onClick={() => onAnswer(challengeId)}>
          Enter the code
        </Button>
      </DialogActions>
    )
  return (
    <Button type="button" variant="primary" onClick={onDone}>
      Done
    </Button>
  )
}
