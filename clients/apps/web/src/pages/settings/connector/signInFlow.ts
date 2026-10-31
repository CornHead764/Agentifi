/**
 * The state machine the bill and shop sign-in dialogs share: the typed
 * credentials, the mailbox watched for a mailed code, the first pull heard
 * from, and where the sign-in stands for the pill.
 */

import { useEffect, useEffectEvent, useRef, useState } from 'react'

import { connectorFailureToast } from '@/components/failure-screenshot'
import type { PullOutcome, SignInPhase } from '@/components/signin/signInTasks'
import { useToast } from '@/components/ui'

import { secondFactorProblem, secondFactorRequest, type SecondFactorChoice } from './secondFactor'

/** The username, password and second factor a sign-in types into the provider's form. */
export function useSignInCredentials(initialUsername: string, initialFactor: SecondFactorChoice) {
  const [username, setUsername] = useState(initialUsername)
  const [password, setPassword] = useState('')
  const [factor, setFactor] = useState(initialFactor)
  const [authKey, setAuthKey] = useState('')
  const keyProblem = secondFactorProblem(factor, authKey)
  return {
    username,
    setUsername,
    password,
    setPassword,
    factor,
    setFactor,
    authKey,
    setAuthKey,
    keyProblem,
    complete: username.trim() !== '' && password !== '' && keyProblem === null,
    secondFactor: () => secondFactorRequest(factor, authKey),
  }
}

export type SignInCredentials = ReturnType<typeof useSignInCredentials>

/**
 * Whether the mailbox's answer should move the dialog on: only while it is
 * still watching. A mailed answer after the person submitted a code would be a
 * second answer to the same question.
 */
export function takesMailedCode(
  result: { mailed_code_found: boolean },
  watching: string | null,
  session: string,
): boolean {
  return result.mailed_code_found && watching !== null && watching === session
}

/**
 * A mailed code (or a text relayed into a mailbox) is answered from the mailbox
 * in the background; whichever answer comes first wins. `codeStep` is the
 * session of a code step worth watching for, each watched once. `stop` is for
 * the person answering themself, or giving up.
 */
export function useMailedCodeWatch<Result extends { mailed_code_found: boolean }>(
  codeStep: string | null,
  ask: (session: string, handlers: { onSuccess: (result: Result) => void; onError: () => void }) => void,
  onCode: (result: Result) => void,
): { watching: string | null; stop: () => void } {
  const [watching, setWatchingState] = useState<string | null>(null)
  const watchingNow = useRef<string | null>(null)
  const setWatching = (session: string | null) => {
    watchingNow.current = session
    setWatchingState(session)
  }
  const watched = useRef(new Set<string>())
  const onCodeStep = useEffectEvent((session: string) => {
    if (watched.current.has(session)) return
    watched.current.add(session)
    setWatching(session)
    ask(session, {
      onSuccess: (result) => {
        const taken = takesMailedCode(result, watchingNow.current, session)
        if (watchingNow.current === session) setWatching(null)
        if (taken) onCode(result)
      },
      onError: () => {
        if (watchingNow.current === session) setWatching(null)
      },
    })
  })
  useEffect(() => {
    if (codeStep !== null) onCodeStep(codeStep)
  }, [codeStep])
  return { watching, stop: () => setWatching(null) }
}

/** The first pull after a kept sign-in: running, and then how it went. */
export interface AfterSignInState {
  phase: Extract<SignInPhase, 'fetching' | 'finished' | 'stopped'>
  note: string
}

/** Null until the sign-in is kept. */
export function afterSignInOf(kept: boolean, outcome: PullOutcome | null): AfterSignInState | null {
  if (!kept) return null
  return {
    phase: outcome === null ? 'fetching' : outcome.ok ? 'finished' : 'stopped',
    note: outcome?.note ?? '',
  }
}

/** Says once how the first pull went, and refreshes what it wrote. */
export function usePullSettledToast(outcome: PullOutcome | null, name: string, refresh: () => void) {
  const { show } = useToast()
  const onPulled = useEffectEvent(() => {
    if (outcome === null) return
    show(
      outcome.ok
        ? { title: `Signed in to ${name}`, description: outcome.note, tone: 'success' }
        : connectorFailureToast({
            title: `Signed in to ${name}; the first update stopped`,
            description: outcome.note,
            provider: name,
            screenshot: outcome.screenshot ?? null,
          }),
    )
    refresh()
  })
  const settled = outcome !== null
  useEffect(() => {
    if (settled) onPulled()
  }, [settled])
}

/** Where this sign-in stands, for the pill that stands in for the dialog. */
export function useReportPhase(
  phase: SignInPhase,
  note: string,
  onPhase: (phase: SignInPhase, note: string) => void,
) {
  const onReport = useEffectEvent(() => onPhase(phase, note))
  useEffect(() => {
    onReport()
  }, [phase, note])
}
