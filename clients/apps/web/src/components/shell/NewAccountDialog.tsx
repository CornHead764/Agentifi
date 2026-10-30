import { useState } from 'react'

import {
  Button,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  Input,
  MoneyInput,
  OptionSelect,
  useToast,
} from '@/components/ui'
import { newAccountBody } from '@/lib/accountDraft'
import { accountTypeOptions } from '@/lib/accountTypes'
import { useCurrentSpace } from '@/lib/clients/spaces'
import { currencyOptions } from '@/lib/currencies'
import { useCreateAccount } from '@/lib/clients/connections'
import { amountFieldError } from '@/lib/money'
import type { AccountWithBalances } from '@/lib/transactions/types'

/**
 * Adding an account by hand. The type list is `accountTypeOptions`, shared
 * with the account details dialog, so a type chosen here can be corrected
 * there.
 *
 * The starting balance is the account's `opening_balance`, which every balance
 * calculation adds to the settled rows. It is not a transaction and does not
 * appear in the register.
 */
export function NewAccountDialog({
  open,
  onOpenChange,
  onCreated,
  initialType = 'checking',
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** For a form that opened this to fill one of its own fields. */
  onCreated?: (account: AccountWithBalances) => void
  /** The type it opens on, for a screen that already knows what kind is wanted. */
  initialType?: string
}) {
  return (
    <FormDialog open={open} onOpenChange={onOpenChange}>
      <NewAccountForm onOpenChange={onOpenChange} onCreated={onCreated} initialType={initialType} />
    </FormDialog>
  )
}

function NewAccountForm({
  onOpenChange,
  onCreated,
  initialType,
}: {
  onOpenChange: (open: boolean) => void
  onCreated?: (account: AccountWithBalances) => void
  initialType: string
}) {
  const { show } = useToast()
  const create = useCreateAccount()

  const [name, setName] = useState('')
  const [type, setType] = useState(initialType)
  // Defaults to the space's currency. Without the picker an account held
  // abroad could only come from the importer, and would be summed into net
  // worth as though it were domestic.
  const space = useCurrentSpace()
  const primary = space.data?.primary_currency ?? 'USD'
  const [currency, setCurrency] = useState<string | null>(null)
  const chosenCurrency = currency ?? primary
  const [balance, setBalance] = useState('')

  // A house and a car are the two types an outside source can price, from the
  // address.
  const [address, setAddress] = useState('')
  const [vin, setVin] = useState('')

  // Shown once a save is tried: a half-typed "1,0" is not a mistake yet.
  const [tried, setTried] = useState(false)
  const balanceError = amountFieldError(balance)

  const submit = () => {
    setTried(true)
    const body = newAccountBody({ name, type, currency: chosenCurrency, balance, address, vin })
    if (body === null) return
    create.mutate(body, {
      onSuccess: (account) => {
        show({ title: `Added ${account.name}`, tone: 'success' })
        onCreated?.(account)
        onOpenChange(false)
      },
    })
  }

  return (
    <DialogContent
      title="New account"
      description="Kept by hand. Link it to SimpleFIN later if the bank is there."
      onSubmit={submit}
      footer={
        <DialogActions>
          <Button type="submit" variant="primary" disabled={create.isPending || name.trim() === ''}>
            {create.isPending ? 'Adding…' : 'Add account'}
          </Button>
        </DialogActions>
      }
    >
      <Field label="Name">
        <Input
          autoFocus
          value={name}
          placeholder="Everyday Checking"
          onChange={(event) => setName(event.target.value)}
        />
      </Field>

      <Field label="Type">
        <OptionSelect
          value={type}
          onValueChange={setType}
          aria-label="Account type"
          options={accountTypeOptions()}
        />
      </Field>

      <Field label="Currency" hint="What this account is held in.">
        <OptionSelect
          value={chosenCurrency}
          onValueChange={setCurrency}
          aria-label="Account currency"
          options={currencyOptions()}
        />
      </Field>

      <Field
        label="Starting balance"
        hint="Optional. The balance before tracking starts; later balances count from it."
        error={tried ? balanceError : null}
      >
        <MoneyInput
          signed
          currency={chosenCurrency}
          value={balance}
          placeholder="0.00"
          onChange={(event) => setBalance(event.target.value)}
        />
      </Field>

      {type === 'real_estate' ? (
        <Field label="Address" hint="What an outside estimate is looked up by.">
          <Input
            value={address}
            placeholder="123 Main St, Springfield, ZZ 00000"
            onChange={(event) => setAddress(event.target.value)}
          />
        </Field>
      ) : null}

      {type === 'vehicle' ? (
        <Field label="VIN" hint="What an outside estimate is looked up by.">
          <Input
            value={vin}
            placeholder="1HGCM82633A004352"
            onChange={(event) => setVin(event.target.value)}
          />
        </Field>
      ) : null}
    </DialogContent>
  )
}
