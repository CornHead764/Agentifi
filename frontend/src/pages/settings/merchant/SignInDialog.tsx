import { useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'

import { ConnectorFailureNote } from '@/components/ConnectorFailureNote'
import {
  cancelLabel,
  leaving,
  merchantPullOutcome,
  phaseOfStep,
  type SignInPhase,
  type SignInViewProps,
} from '@/components/signin/signInTasks'
import { Button, Dialog, DialogActions, DialogContent, Field, Input } from '@/components/ui'
import {
  describeSync,
  useAnswerMerchantSignIn,
  useCompleteMerchantSignIn,
  useCreateMerchantAccount,
  useDeleteMerchantAccount,
  useMailedMerchantCode,
  useMerchantPullAfterSignIn,
  useMerchantSignInStatus,
  useStartMerchantSignIn,
  merchantRootKey,
  type MerchantAccount,
  type MerchantMailedCode,
  type MerchantSignIn,
} from '@/lib/clients/merchant'
import { MERCHANTS, type MerchantId } from '@/lib/merchants'
import type { Uuid } from '@/lib/transactions/types'

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
import { PullProgressLine } from './PullProgressLine'

/**
 * Signing in to a shop, and adding a login in the same breath. The typed email
 * and password are played into the shop's own form (the browser is the
 * server's choice). The session and the password are kept, the password
 * sealed onto the account only once the sign-in lands.
 *
 * With no account the dialog makes one on the first press, and removes it if
 * the dialog is abandoned before a sign-in lands. Closing a running sign-in is
 * not abandoning it: the shell hosts the dialog (`SignInHost`) and it becomes a
 * pill. Only Cancel gives it up.
 */
export function SignInDialog({
  merchant,
  account,
  minimized,
  onMinimize,
  onPhase,
  onClose,
}: SignInViewProps & {
  merchant: MerchantId
  /** The login to sign in again; null adds a new one. */
  account: MerchantAccount | null
}) {
  const { name, nounPlural, signInAdvice } = MERCHANTS[merchant]
  const client = useQueryClient()
  const [label, setLabel] = useState('')
  const credentials = useSignInCredentials(
    account?.email ?? '',
    initialSecondFactor(account?.second_factor, false),
  )
  const email = credentials.username
  const [code, setCode] = useState('')
  const [step, setStep] = useState<MerchantSignIn | null>(null)
  // The account this dialog made, until a sign-in keeps it.
  const made = useRef<Uuid | null>(null)
  const create = useCreateMerchantAccount(merchant)
  const remove = useDeleteMerchantAccount(merchant)
  const start = useStartMerchantSignIn(merchant)
  const answer = useAnswerMerchantSignIn(merchant)
  const status = useMerchantSignInStatus(merchant)
  const complete = useCompleteMerchantSignIn(merchant)
  const mailed = useMailedMerchantCode(merchant)
  const busy =
    create.isPending ||
    start.isPending ||
    answer.isPending ||
    status.isPending ||
    complete.isPending

  const id = account?.id ?? made.current
  // The session the finished sign-in kept, which the pull it started is
  // polled under; null until then.
  const [kept, setKept] = useState<{ id: Uuid; session: string } | null>(null)

  const finish = (accountID: Uuid, session: string) =>
    complete.mutate(
      { id: accountID, session, email },
      {
        onSuccess: (signedIn) => {
          made.current = null
          if (signedIn.pulling) {
            setKept({ id: accountID, session })
            return
          }
          onClose()
        },
      },
    )

  const pulled = useMerchantPullAfterSignIn(merchant, kept?.id ?? null, kept?.session ?? null)
  const outcome =
    pulled.data === undefined
      ? null
      : merchantPullOutcome(pulled.data, merchant, () => describeSync(merchant, pulled.data))
  const after = afterSignInOf(kept !== null, outcome)
  // A pull files orders and matches bank rows, which every merchant's screens
  // and the register's purchases read.
  usePullSettledToast(outcome, name, () => void client.invalidateQueries({ queryKey: merchantRootKey }))

  const advance = (accountID: Uuid) => (next: MerchantSignIn) => {
    setStep(next)
    setCode('')
    if (next.state === 'signed_in') finish(accountID, next.session_id)
  }

  const mailbox = useMailedCodeWatch<MerchantMailedCode>(
    step?.state === 'otp' ? step.session_id : null,
    (session, handlers) => {
      const accountID = account?.id ?? made.current
      if (accountID !== null) mailed.mutate({ id: accountID, session }, handlers)
    },
    (result) => {
      const accountID = account?.id ?? made.current
      if (accountID !== null) advance(accountID)(result)
    },
  )

  const signIn = (accountID: Uuid) =>
    start.mutate(
      {
        id: accountID,
        email: email.trim(),
        password: credentials.password,
        ...credentials.secondFactor(),
      },
      { onSuccess: advance(accountID) },
    )

  const submit = () => {
    if (!step || step.state === 'failed') {
      if (id) {
        signIn(id)
        return
      }
      create.mutate(label.trim(), {
        onSuccess: (created) => {
          made.current = created.id
          signIn(created.id)
        },
      })
      return
    }
    if (!id) return
    if (step.state === 'approval') {
      status.mutate({ id, session: step.session_id }, { onSuccess: advance(id) })
      return
    }
    mailbox.stop()
    answer.mutate({ id, session: step.session_id, code: code.trim() }, { onSuccess: advance(id) })
  }

  const typing = !step || step.state === 'failed'
  const asking = step && (step.state === 'otp' || step.state === 'captcha')

  // Where this sign-in stands, for the pill that stands in for the dialog.
  const phase: SignInPhase = after?.phase ?? phaseOfStep(step?.state ?? null, busy)
  useReportPhase(phase, after?.note ?? '', onPhase)

  const leave = (how: 'close' | 'cancel') => {
    const { cancel, then } = leaving(phase, how)
    if (then === 'minimize') {
      onMinimize()
      return
    }
    if (cancel) {
      mailbox.stop()
      if (made.current) remove.mutate(made.current)
    }
    onClose()
  }

  return (
    <Dialog open={!minimized} onOpenChange={(open) => !open && leave('close')}>
      <DialogContent
        title={
          account
            ? `Sign in to ${name}${account.label ? ` · ${account.label}` : ''}`
            : `Add a ${name} account`
        }
        description={signInAdvice}
        onSubmit={(event) => {
          event.preventDefault()
          submit()
        }}
        footer={
          after !== null ? (
            <AfterSignInFooter after={after} onClose={() => leave('close')} onDone={onClose} />
          ) : (
            <DialogActions cancel={cancelLabel(phase)} onCancel={() => leave('cancel')}>
              <Button
                type="submit"
                variant="primary"
                disabled={busy || (typing && !credentials.complete)}
              >
                {busy
                  ? `Waiting for ${name}…`
                  : typing
                    ? 'Sign in'
                    : step?.state === 'approval'
                      ? 'I approved it, continue'
                      : 'Continue'}
              </Button>
            </DialogActions>
          )
        }
      >
        {after !== null ? (
          <AfterSignIn
            after={after}
            fetching={
              <PullProgressLine
                progress={pulled.data?.progress}
                fallback={`Signed in. Fetching your ${nounPlural} from ${name}…`}
              />
            }
          />
        ) : null}
        {after === null && start.isPending ? (
          <SignInWorking line={`Signing in at ${name}`} />
        ) : null}
        {after === null && typing ? (
          <>
            {step?.state === 'failed' ? (
              <ConnectorFailureNote
                raw={step.error || step.prompt || `${name} refused the sign-in.`}
                provider={name}
                screenshot={step.image ? { image: step.image } : null}
              />
            ) : null}
            {account ? null : (
              <Field
                label="Name (optional)"
                hint={`To tell ${name} logins apart. Blank uses the email.`}
              >
                <Input
                  value={label}
                  onChange={(event) => setLabel(event.target.value)}
                  placeholder="Alex"
                  maxLength={80}
                />
              </Field>
            )}
            <SignInCredentialsFields
              provider={name}
              userLabel={`${name} email or phone`}
              credentials={credentials}
            />
          </>
        ) : null}
        {after === null && asking ? (
          <SignInCodeStep
            provider={name}
            prompt={step.prompt}
            captcha={step.state === 'captcha'}
            image={step.image}
            code={code}
            onCode={setCode}
          />
        ) : null}
        {after === null && step?.state === 'otp' && mailbox.watching === step.session_id ? (
          <p className="muted">Watching the mailbox for the code…</p>
        ) : null}
        {after === null && step?.state === 'approval' ? <p>{step.prompt}</p> : null}
        {after === null && step?.state === 'signed_in' ? <SignedInNote /> : null}
      </DialogContent>
    </Dialog>
  )
}
