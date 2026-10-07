import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useEffectEvent, useRef, useState } from 'react'

import { ConnectorFailureNote } from '@/components/ConnectorFailureNote'
import {
  billPullOutcome,
  cancelLabel,
  leaving,
  phaseOfStep,
  type SignInPhase,
  type SignInViewProps,
} from '@/components/signin/signInTasks'
import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  useToast,
} from '@/components/ui'
import { maskedNumber } from '@/lib/accounts'
import { billerById, billerName, connectionName, connectionTitle } from '@/lib/billers'
import {
  BILLS_KEY,
  billSignInKey,
  cancelBillSignIn,
  challengeInstructions,
  describePullStatus,
  fetchBillSignInTrail,
  useAnswerBillSignIn,
  useBillChallenges,
  useBillPullAfterSignIn,
  useBillSignInStatus,
  useCompleteBillSignIn,
  useMailedBillCode,
  useReleaseBillBrowser,
  useStartBillSignIn,
  type BillConnection,
  type BillMailedCode,
  type BillProviderInfo,
  type BillSignInState,
  type BillTrailEntry,
} from '@/lib/clients/bills'
import { invalidateOccurrenceWrites } from '@/lib/clients/upcoming/keys'
import { plural } from '@/lib/format'

import { initialSecondFactor } from '../connector/secondFactor'
import {
  afterSignInOf,
  useMailedCodeWatch,
  usePullSettledToast,
  useReportPhase,
  useSignInCredentials,
} from '../connector/signInFlow'
import {
  AfterSignIn,
  AfterSignInFooter,
  SignedInNote,
  SignInCodeStep,
  SignInCredentialsFields,
  SignInWorking,
} from '../connector/SignInSteps'
import { ChallengeDialog } from './ChallengeDialog'
import { SiteField } from './SiteField'
import { signInStuck, signInSubmit } from './signIn'
import { ProviderTrail } from './ProviderTrail'
import { waitingChallengeFor } from './status'

/**
 * Signing in to a provider through the agent: the username and password are
 * typed into the provider's own form and kept, encrypted, so every nightly
 * pull can sign in again; an authenticator setup key lets the agent mint codes.
 * Only the typed form is offered, because a kept session alone stops pulling
 * when it lapses.
 *
 * Hosted by the shell (`SignInHost`), so it can be minimized to a pill.
 */
export function ConnectDialog({
  connection,
  provider,
  minimized,
  onMinimize,
  onPhase,
  onClose,
}: SignInViewProps & {
  connection: BillConnection
  /** What the agent says this provider can do. Null is an agent that has none. */
  provider: BillProviderInfo | null
}) {
  const { show } = useToast()
  const name = provider?.name ?? billerName(connection.biller)
  /**
   * A provider deployed once per customer asks which deployment. It goes on
   * the connection, because the nightly pull and the keepalive open the same
   * address.
   */
  const siteField = billerById(connection.biller)?.site
  const credentials = useSignInCredentials(
    connection.username,
    initialSecondFactor(connection.second_factor, provider?.challenges.includes('totp') === true),
  )
  const [site, setSite] = useState(connection.site)
  const [code, setCode] = useState('')
  const [session, setSession] = useState<string | null>(null)
  // The failed state usually carries its own trail; this is the fallback for a
  // failure that did not (the agent reaps an idle session after twenty minutes).
  const [asked, setAsked] = useState<BillTrailEntry[]>([])
  // The session the finished sign-in kept, which the pull it started is
  // polled under; null until then. `summary` is what the login bills for.
  const [sealed, setSealed] = useState<{ session: string; summary: string } | null>(null)

  const client = useQueryClient()
  const start = useStartBillSignIn()
  const answer = useAnswerBillSignIn()
  const complete = useCompleteBillSignIn()
  const release = useReleaseBillBrowser()

  /**
   * Give up the browser behind a refusal. A lock a restart left on the server
   * has no session for this dialog to cancel, so dismissing does nothing; this
   * clears the hold. It also closes whatever session was being polled.
   */
  const releaseBrowser = () =>
    release.mutate(connection.id, {
      onSuccess: (given) => {
        show({ title: given.message })
        setSession(null)
      },
    })

  /**
   * An answer is written into the poll's own cache rather than held beside it:
   * two copies of a step is how the code box shows after the code was accepted.
   */
  const status = useBillSignInStatus(connection.id, session)
  const step = status.data ?? null

  // Kept once: a poll landing twice on "signed in" would complete twice, and
  // the second is a 404 on a consumed session.
  const kept = useRef(false)
  const finish = (sessionId: string) => {
    if (kept.current) return
    kept.current = true
    complete.mutate(
      { connectionId: connection.id, session: sessionId },
      {
        onSuccess: (connected) => {
          const summary =
            connected.subaccounts.length === 0
              ? 'Nothing is billed under this login yet.'
              : `${plural(connected.subaccounts.length, 'account')}: ${connected.subaccounts
                  .map((one) => one.label)
                  .join(', ')}`
          if (connected.pulling) {
            setSealed({ session: sessionId, summary })
            return
          }
          show({ title: `Signed in to ${connectionName(connected)}`, description: summary })
          onClose()
        },
        onError: () => {
          kept.current = false
        },
      },
    )
  }

  // The first pull, heard from: the row it writes to says when it has
  // finished and how it went.
  const pulled = useBillPullAfterSignIn(connection.id, sealed?.session ?? null)
  const outcome =
    pulled.data === undefined
      ? null
      : billPullOutcome(pulled.data, name, () => describePullStatus(pulled.data))
  // The code request the first pull parked, answered here rather than on the
  // Bills screen: this dialog is where the person already is.
  const challenges = useBillChallenges()
  const waiting =
    outcome?.asksForCode === true
      ? waitingChallengeFor(connection.id, challenges.data ?? [])
      : null
  const [answering, setAnswering] = useState<string | null>(null)
  const after = afterSignInOf(sealed !== null, outcome)
  usePullSettledToast(outcome, name, () => {
    void client.invalidateQueries({ queryKey: BILLS_KEY })
    invalidateOccurrenceWrites(client)
  })

  const advance = (next: BillSignInState) => {
    setSession(next.session_id)
    setCode('')
    client.setQueryData(billSignInKey(connection.id, next.session_id), next)
  }

  // An authenticator's code is never mailed, so its step is not watched.
  const mailed = useMailedBillCode()
  const mailbox = useMailedCodeWatch<BillMailedCode>(
    step !== null && step.state === 'otp' && step.method !== 'totp' ? step.session_id : null,
    (sessionId, handlers) => mailed.mutate({ connectionId: connection.id, session: sessionId }, handlers),
    advance,
  )

  const landed = step?.state === 'signed_in' ? step.session_id : null
  const onLanded = useEffectEvent((sessionId: string) => finish(sessionId))
  useEffect(() => {
    if (landed !== null) onLanded(landed)
  }, [landed])

  /**
   * The sign-in is over: `failed`, or a state this dialog has no screen for
   * (the agent settles those, and waiting would show an empty dialog). Either
   * way the typed form comes back, under what the provider showed.
   */
  const stuck = step !== null && signInStuck(step.state)
  const over = step !== null && (step.state === 'failed' || stuck)

  // A failure with no trail may still have an open session; ask the agent for
  // it once.
  const failedSession = over && step !== null ? step.session_id : null
  const carried = over ? (step?.trail ?? []) : []
  const trail = carried.length > 0 ? carried : asked
  useEffect(() => {
    if (failedSession === null || carried.length > 0) return
    let dropped = false
    void fetchBillSignInTrail(connection.id, failedSession)
      .then((rounds) => {
        if (!dropped) setAsked(rounds)
      })
      .catch(() => {})
    return () => {
      dropped = true
    }
    // The state object is new on every poll; the session and whether it
    // brought a trail are what change.
  }, [failedSession, carried.length, connection.id])

  /**
   * What the provider is asking, in its own words where it said any. A code
   * request that does not name its channel is asked for plainly rather than
   * guessed at as a text.
   */
  const askedFor = (asked: BillSignInState): string => {
    if (asked.prompt.trim() !== '') return asked.prompt.trim()
    if (asked.state === 'captcha') return challengeInstructions('captcha', '')
    if (asked.state === 'approval') return challengeInstructions('push', '')
    if (asked.method !== '') return challengeInstructions(asked.method, '')
    return 'Enter the code the provider is asking for.'
  }

  const { keyProblem } = credentials
  const busy = start.isPending || answer.isPending || complete.isPending
  /**
   * The agent is driving the provider's pages and nobody is being asked
   * anything. Polled rather than one POST held open for most of a minute;
   * `prompt` is the agent's own account of what it is doing.
   */
  const working = step !== null && step.state === 'signing_in'
  const asking = step !== null && (step.state === 'otp' || step.state === 'captcha')
  const listing = step !== null && step.state === 'accounts'

  // Where this sign-in stands, for the pill that stands in for the dialog.
  const phase: SignInPhase = after?.phase ?? phaseOfStep(step?.state ?? null, busy)
  const note =
    after === null
      ? listing
        ? `${name}: signed in — open to keep it`
        : ''
      : after.note
  useReportPhase(phase, note, onPhase)

  /**
   * Closing puts a running sign-in away and keeps it running; only Cancel gives
   * it up. The agent holds one browser per connection, so an abandoned sign-in
   * would refuse the next attempt until its reaper ran. A completed session is
   * never given up: it is the connection's now.
   */
  const leave = (how: 'close' | 'cancel') => {
    const { cancel, then } = leaving(phase, how)
    if (cancel && session !== null && !kept.current)
      void cancelBillSignIn(connection.id, session).catch(() => {})
    if (then === 'minimize') onMinimize()
    else onClose()
  }

  const submit = () => {
    switch (signInSubmit({ busy, state: step?.state ?? null, keyProblem: keyProblem !== null })) {
      case 'nothing':
        return
      case 'finish':
        if (step !== null) finish(step.session_id)
        return
      case 'refetch':
        void status.refetch()
        return
      case 'start': {
        start.mutate(
          {
            connectionId: connection.id,
            credentials: {
              username: credentials.username.trim(),
              password: credentials.password,
              ...(site.trim() === '' ? {} : { site: site.trim() }),
              ...credentials.secondFactor(),
            },
          },
          { onSuccess: advance },
        )
        return
      }
      case 'answer':
        if (step === null) return
        mailbox.stop()
        answer.mutate(
          { connectionId: connection.id, session: step.session_id, code: code.trim() },
          { onSuccess: advance },
        )
    }
  }

  /**
   * What the agent is doing. The wait starts before there is a session to poll,
   * since opening a browser at the provider takes several seconds.
   */
  const signingInLine = () => {
    if (working && step !== null && step.prompt.trim() !== '') return step.prompt
    return `Opening a browser at ${name}`
  }

  const action = () => {
    if (complete.isPending) return 'Keeping the session…'
    if (busy || working) return `Waiting for ${name}…`
    if (step === null || over) return 'Sign in'
    if (step.state === 'approval') return 'I approved it, continue'
    return 'Continue'
  }

  if (answering !== null) {
    return (
      <ChallengeDialog
        challengeId={answering}
        connection={connection}
        onClose={onClose}
        onSignInAgain={() => setAnswering(null)}
      />
    )
  }

  return (
    <Dialog open={!minimized} onOpenChange={(open) => (open ? undefined : leave('close'))}>
      <DialogContent
        title={`Sign in to ${connectionTitle(connection)}`}
        description="Kept encrypted so later updates can sign in on their own."
        onSubmit={(event) => {
          event.preventDefault()
          submit()
        }}
        footer={
          after !== null ? (
            <AfterSignInFooter
              after={after}
              onClose={() => leave('close')}
              onDone={onClose}
              challengeId={waiting?.id ?? null}
              onAnswer={setAnswering}
            />
          ) : (
            <DialogActions cancel={cancelLabel(phase)} onCancel={() => leave('cancel')}>
              <Button
                type="submit"
                variant="primary"
                disabled={
                  busy ||
                  working ||
                  (siteField !== undefined && site.trim() === '') ||
                  (step === null && !credentials.complete)
                }
              >
                {action()}
              </Button>
            </DialogActions>
          )
        }
      >
        {after !== null ? (
          <AfterSignIn
            after={after}
            fetching={`Signed in. Fetching your bills from ${name}…`}
            summary={sealed?.summary}
          />
        ) : null}

        {after === null && (working || start.isPending) ? (
          <SignInWorking line={signingInLine()} />
        ) : null}

        {after === null && (step === null || over) ? (
          <>
            {step !== null && over ? (
              <ConnectorFailureNote
                raw={
                  stuck
                    ? `unanswerable state: ${step.state}`
                    : step.error || step.prompt || `${name} refused the sign-in.`
                }
                provider={name}
                screenshot={step.image ? { image: step.image } : null}
              />
            ) : null}
            {step !== null && over ? (
              // Clears a browser hold a restart left behind. The kept password
              // and session stay.
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={release.isPending}
                onClick={releaseBrowser}
              >
                {release.isPending
                  ? 'Releasing the browser…'
                  : 'Release the browser this connection is holding'}
              </Button>
            ) : null}
            {over && trail.length > 0 ? <ProviderTrail trail={trail} /> : null}
            {siteField === undefined ? null : (
              <SiteField site={siteField} value={site} onChange={setSite} />
            )}
            <SignInCredentialsFields
              provider={name}
              userLabel={`${name} username`}
              credentials={credentials}
            />
          </>
        ) : null}

        {after === null && asking && step !== null ? (
          <SignInCodeStep
            provider={name}
            prompt={askedFor(step)}
            captcha={step.state === 'captcha'}
            image={step.image}
            code={code}
            onCode={setCode}
          />
        ) : null}

        {after === null && step?.state === 'otp' && mailbox.watching === step.session_id ? (
          <p className="muted">Watching the mailbox for the code…</p>
        ) : null}

        {after === null && step?.state === 'approval' ? <p>{askedFor(step)}</p> : null}

        {after === null && listing && step !== null ? (
          <>
            <p>
              {`${plural(step.accounts?.length ?? 0, 'account')} under this login. All come across; untick any on the card later.`}
            </p>
            <ul className="bill-accounts">
              {(step.accounts ?? []).map((one) => (
                <li key={one.external_id}>
                  {one.label}
                  {one.masked_number === '' ? '' : ` · ${maskedNumber(one.masked_number)}`}
                </li>
              ))}
            </ul>
          </>
        ) : null}

        {after === null && step?.state === 'signed_in' ? <SignedInNote /> : null}
      </DialogContent>
    </Dialog>
  )
}
