import { useState } from 'react'

import {
  Button,
  Callout,
  Dialog,
  DialogActions,
  DialogContent,
  useProgressToast,
} from '@/components/ui'
import { billerName, connectionTitle } from '@/lib/billers'
import {
  challengeInstructions,
  describePullResult,
  useAnswerBillChallenge,
  useBillChallenge,
  type BillConnection,
} from '@/lib/clients/bills'
import type { Uuid } from '@/lib/clients/entities'
import { timeAgo } from '@/lib/format'

import { SignInCodeStep } from '../connector/SignInSteps'

/**
 * The one modal for every second factor a pull can stop on. A push
 * notification links here as `/settings/bills?challenge=<id>`, and the
 * connection card opens it too. Answering resumes the pull that stopped.
 */
export function ChallengeDialog({
  challengeId,
  connection,
  onClose,
  onSignInAgain,
}: {
  challengeId: Uuid
  /** The login it belongs to, when this screen has it. Null while the list is loading. */
  connection: BillConnection | null
  onClose: () => void
  /** Offered for an expired challenge: the session behind it is gone. */
  onSignInAgain: () => void
}) {
  const progress = useProgressToast()
  const [code, setCode] = useState('')
  const answer = useAnswerBillChallenge()

  // Nothing here hears a push approval: the row changes, so it is polled while
  // it waits and the dialog closes itself when it stops waiting.
  const challenge = useBillChallenge(challengeId, true)
  const waiting = challenge.data
  const method = waiting?.method ?? 'sms'
  const who = connection === null ? 'this provider' : billerName(connection.biller)

  const send = async () => {
    if (!waiting || waiting.state !== 'waiting') return
    const provider = connection ? billerName(connection.biller) : undefined
    const result = await progress(
      `Updating ${who}…`,
      answer.mutateAsync({ challengeId, code: method === 'push' ? '' : code.trim() }),
      (result) => ({
        title: describePullResult(result, provider),
        description: result.notes[0],
        tone: result.status === 'failed' ? 'error' : undefined,
      }),
      provider === undefined ? 'The update did not finish' : `${provider} was not updated`,
    )
    if (result) onClose()
  }

  const expired = waiting?.state === 'expired'
  const answered = waiting?.state === 'answered'
  const typed = method !== 'push'

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent
        title={
          connection === null
            ? 'A bill sign-in needs a code'
            : `${connectionTitle(connection)} needs a code`
        }
        description={
          expired
            ? 'This request has expired. Start the sign-in again.'
            : 'The provider asked for this before it would show the bills. Answering finishes the update.'
        }
        onSubmit={(event) => {
          event.preventDefault()
          void send()
        }}
        footer={
          <DialogActions cancel="Close">
            {expired ? (
              <Button type="button" variant="primary" onClick={onSignInAgain}>
                Sign in again
              </Button>
            ) : (
              <Button
                type="submit"
                variant="primary"
                disabled={
                  answer.isPending ||
                  !waiting ||
                  waiting.state !== 'waiting' ||
                  (typed && code.trim() === '')
                }
              >
                {answer.isPending
                  ? 'Finishing the update…'
                  : typed
                    ? 'Answer and finish the update'
                    : 'I approved it, continue'}
              </Button>
            )}
          </DialogActions>
        }
      >
        {challenge.isPending ? <p className="muted">Reading the request…</p> : null}
        {challenge.isError ? (
          <Callout tone="warning">
            This request could not be read. It may have been answered already.
          </Callout>
        ) : null}
        {answered ? (
          <p>Already answered. The update it paused has carried on without you.</p>
        ) : null}

        {waiting && waiting.state === 'waiting' ? (
          <>
            {typed ? (
              <SignInCodeStep
                provider={who}
                prompt={challengeInstructions(method, waiting.prompt)}
                captcha={method === 'captcha'}
                image={waiting.image}
                code={code}
                onCode={setCode}
              />
            ) : (
              <p>{challengeInstructions(method, waiting.prompt)}</p>
            )}
            <p className="muted">
              {`Asked for ${timeAgo(waiting.created_at, 'long')}. Codes expire within minutes; if refused, sign in again.`}
            </p>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
