/**
 * Adding a position by hand. Holdings arrive from the Simplifi import and
 * nowhere else: SimpleFIN can parse them but the sync does not persist them.
 *
 * **A symbol with no price on file has to say what it is worth.** Nothing
 * fetches quotes, and the server refuses a holding with neither a price nor a
 * value. The field appears only once the symbol is known not to be priced.
 */

import { useState } from 'react'

import {
  Button,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  Input,
  MoneyInput,
  useToast,
} from '@/components/ui'
import { AccountSelect } from '@/components/AccountSelect'
import { holdingDraftFrom, useAddHolding, useSecurities } from '@/lib/clients/investments'
import { amountFieldError } from '@/lib/money'
import type { Account } from '@/lib/transactions/types'

export function AddHoldingDialog({
  open,
  onOpenChange,
  accounts,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Investment accounts only — nothing else can hold a position. */
  accounts: readonly Account[]
}) {
  return (
    <FormDialog open={open} onOpenChange={onOpenChange}>
      <AddHoldingForm onOpenChange={onOpenChange} accounts={accounts} />
    </FormDialog>
  )
}

function AddHoldingForm({
  onOpenChange,
  accounts,
}: {
  onOpenChange: (open: boolean) => void
  accounts: readonly Account[]
}) {
  const { show } = useToast()
  const securities = useSecurities()
  const add = useAddHolding()

  const [accountId, setAccountId] = useState(accounts[0]?.id ?? '')
  const [symbol, setSymbol] = useState('')
  const [name, setName] = useState('')
  const [shares, setShares] = useState('')
  const [costBasis, setCostBasis] = useState('')
  const [marketValue, setMarketValue] = useState('')

  const typed = symbol.trim().toUpperCase()
  const known = (securities.data ?? []).find((one) => one.symbol === typed)
  // Priced means the server will value it from the share count. Unknown to
  // this space, or known but never quoted, means it cannot.
  const priced = known !== undefined && known.last_price !== null
  const needsValue = typed !== '' && !priced

  const ready =
    accountId !== '' && typed !== '' && shares.trim() !== '' && (!needsValue || marketValue.trim() !== '')

  // Shown once a save is tried: a half-typed "1,0" is not a mistake yet.
  const [tried, setTried] = useState(false)
  const costBasisError = tried ? amountFieldError(costBasis) : null
  const marketValueError = tried ? amountFieldError(marketValue) : null

  const submit = () => {
    setTried(true)
    const draft = holdingDraftFrom({
      accountId,
      symbol,
      name,
      shares,
      costBasis,
      marketValue,
      needsValue,
    })
    if (draft === null) return
    add.mutate(draft, {
      onSuccess: () => {
        show({ title: `Added ${typed}`, tone: 'success' })
        onOpenChange(false)
      },
    })
  }

  return (
    <DialogContent
      title="New holding"
      description="For a position no connection syncs."
      onSubmit={submit}
      footer={
        <DialogActions>
          <Button type="submit" variant="primary" disabled={!ready || add.isPending}>
            {add.isPending ? 'Adding…' : 'Add holding'}
          </Button>
        </DialogActions>
      }
    >
      <Field label="Account">
        <AccountSelect accounts={accounts} value={accountId} onValueChange={setAccountId} />
      </Field>

      <div className="form-row">
        <Field
          label="Symbol"
          hint={known ? known.name : 'Its ticker, or whatever the plan calls the fund.'}
        >
          <Input
            value={symbol}
            onChange={(event) => setSymbol(event.target.value)}
            placeholder="VTSAX"
            autoComplete="off"
          />
        </Field>
        <Field label="Shares" hint="A fractional position is real; enter it as typed.">
          <Input
            numeric
            inputMode="decimal"
            value={shares}
            onChange={(event) => setShares(event.target.value)}
            placeholder="10.5"
          />
        </Field>
      </div>

      {/* Only for a symbol this space has not held: overwriting an existing
          name would rename it for every account holding it. */}
      {typed !== '' && known === undefined ? (
        <Field label="Name" hint="Optional. The symbol stands in when this is blank.">
          <Input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="Total Stock Market Index Fund"
          />
        </Field>
      ) : null}

      <div className="form-row">
        <Field
          label="Cost basis"
          hint="Optional. Without it the gain is unknown rather than zero."
          error={costBasisError}
        >
          <MoneyInput
            value={costBasis}
            onChange={(event) => setCostBasis(event.target.value)}
            placeholder="0.00"
          />
        </Field>

        {needsValue ? (
          <Field
            label="Value"
            hint={`No price on file for ${typed}. Enter its total value.`}
            error={marketValueError}
          >
            <MoneyInput
              value={marketValue}
              onChange={(event) => setMarketValue(event.target.value)}
              placeholder="0.00"
            />
          </Field>
        ) : null}
      </div>
    </DialogContent>
  )
}
