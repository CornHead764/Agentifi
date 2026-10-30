/**
 * Moving money between two of your own accounts: both legs written, then
 * paired, indistinguishable from a detected transfer. The pair is written
 * last, and a refusal there would leave two unpaired rows counted as income
 * and expense, so every server rule is checked before the first request.
 */

import { useState } from 'react'

import {
  Button,
  Callout,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  Input,
  MoneyInput,
  useToast,
} from '@/components/ui'
import { AccountSelect } from '@/components/AccountSelect'
import { manualTransactionTargets } from '@/lib/accounts'
import { useRecordTransfer } from '@/lib/clients/transfers'
import { toIsoDate } from '@/lib/format'
import { describeApiError } from '@/lib/transactions/queries'
import {
  blankTransferDraft,
  describeTransferProblem,
  planTransfer,
  transferProblem,
  type TransferDraft,
} from '@/lib/transactions/transferMoney'
import type { Account } from '@/lib/transactions/types'

export function TransferMoneyDialog({
  open,
  onOpenChange,
  from,
  accounts,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The account the menu was opened on, which the money leaves by default. */
  from: Account
  accounts: readonly Account[]
  onDone: () => void
}) {
  const { show } = useToast()
  const record = useRecordTransfer()
  const [draft, setDraft] = useState<TransferDraft>(() =>
    blankTransferDraft(from.id, toIsoDate(new Date())),
  )
  const saving = record.isPending
  const set = (patch: Partial<TransferDraft>) => setDraft((current) => ({ ...current, ...patch }))

  // Not closed, not a gift-card balance the Amazon pull owns, not a
  // SimpleFIN-synced account: the next sync would neither know nor match a
  // leg written there.
  const open_accounts = manualTransactionTargets(accounts)
  const problem = transferProblem(draft, open_accounts)
  const touched = draft.toAccountId !== '' || draft.amount !== ''

  const submit = () => {
    const plan = planTransfer(draft, open_accounts)
    if (plan === null || saving) return
    record.mutate(plan, {
      onSuccess: () => {
        show({ title: 'Transfer recorded', tone: 'success' })
        onDone()
        onOpenChange(false)
      },
      // Both legs may already be in the register, and saying so is how the
      // user can find and pair or delete them.
      onError: (error) =>
        show({
          title: 'The transfer did not complete',
          description: `${describeApiError(error)} Check the register for unpaired halves.`,
          tone: 'error',
        }),
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        title="Transfer money"
        description="Records both halves, paired. Neither counts as income or spending."
        onSubmit={submit}
        footer={
          <DialogActions>
            <Button type="submit" variant="primary" disabled={saving || problem !== null}>
              Record transfer
            </Button>
          </DialogActions>
        }
      >
        <div className="form-row">
          <Field label="From">
            <AccountSelect
              accounts={open_accounts}
              value={draft.fromAccountId}
              onValueChange={(value) => set({ fromAccountId: value })}
            />
          </Field>

          <Field label="To">
            <AccountSelect
              accounts={open_accounts.filter((account) => account.id !== draft.fromAccountId)}
              value={draft.toAccountId}
              onValueChange={(value) => set({ toAccountId: value })}
            />
          </Field>
        </div>

        <div className="form-row">
          <Field
            label="Amount"
            hint="Signs are set per leg."
          >
            <MoneyInput
              placeholder="0.00"
              value={draft.amount}
              onChange={(event) => set({ amount: event.target.value })}
            />
          </Field>
          <Field label="Date">
            <Input
              type="date"
              value={draft.date}
              onChange={(event) => set({ date: event.target.value })}
            />
          </Field>
        </div>

        <Field label="Note" hint="Optional. Written on both halves.">
          <Input value={draft.notes} onChange={(event) => set({ notes: event.target.value })} />
        </Field>

        {/* Only once something has been chosen: an empty form is not yet wrong. */}
        {problem !== null && touched ? (
          <Callout tone="expense">{describeTransferProblem(problem)}</Callout>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
