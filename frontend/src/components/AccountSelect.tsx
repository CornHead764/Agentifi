import { useState } from 'react'

import { smallBalancesLabel } from '@/components/shell/accountTree'
import { OptionSelect } from '@/components/ui'
import { pickerAccounts } from '@/lib/accounts'

export interface AccountSelectProps {
  /** An account flagged `hidden_small_balance` is left out until revealed, unless it is the value. */
  accounts: readonly { id: string; name: string; hidden_small_balance?: boolean }[]
  value: string
  onValueChange: (id: string) => void
  /** A choice listed above the accounts that is not one of them: "Every account", "No account". */
  firstOption?: { value: string; label: string }
  placeholder?: string
  disabled?: boolean
  className?: string
  'aria-label'?: string
}

/** One account picked from a list, by name. Inside a `Field`, it takes the field's label. */
export function AccountSelect({
  accounts,
  firstOption,
  placeholder = 'Select account',
  value,
  ...props
}: AccountSelectProps) {
  const [revealed, setRevealed] = useState(false)
  const { shown, hidden } = pickerAccounts(accounts, value, revealed)
  const options = shown.map((account) => ({ value: account.id, label: account.name }))
  return (
    <OptionSelect
      options={firstOption ? [firstOption, ...options] : options}
      placeholder={placeholder}
      value={value}
      action={
        hidden === 0
          ? undefined
          : { label: smallBalancesLabel(hidden, revealed), onSelect: () => setRevealed(!revealed) }
      }
      {...props}
    />
  )
}
