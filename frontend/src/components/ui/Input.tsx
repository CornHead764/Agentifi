import { clsx } from 'clsx'
import type { ComponentProps, ReactNode } from 'react'

import { currencySymbol } from '@/lib/currencies'
import { displayCurrency } from '@/lib/money'

import { useFieldControl } from './field-context'

export interface InputProps extends Omit<ComponentProps<'input'>, 'size'> {
  /** `sm` in a table row or toolbar, beside `size="sm"` buttons. */
  size?: 'sm' | 'md'
  /** Right-aligned tabular digits, for amounts, quantities and check numbers. */
  numeric?: boolean
  /**
   * A fixed leading glyph — the currency symbol on an amount input. Named
   * `leading` because `prefix` is a real HTML attribute with another meaning.
   */
  leading?: ReactNode
}

export function Input({ size = 'md', numeric = false, leading, className, ...props }: InputProps) {
  const field = useFieldControl()
  const input = (
    <input
      className={clsx(
        'input',
        size === 'sm' && 'input--sm',
        numeric && 'input--numeric',
        !leading && className,
      )}
      {...field}
      {...props}
    />
  )

  if (!leading) return input

  return (
    <span className={clsx('input-affix', className)}>
      {leading}
      {input}
    </span>
  )
}

export interface MoneyInputProps extends Omit<InputProps, 'numeric' | 'leading'> {
  /** The amount's currency; the household's display currency when omitted. */
  currency?: string
  /** A decimal keypad has no minus key on iOS, so a signed amount keeps the full keyboard. */
  signed?: boolean
}

export function MoneyInput({ currency, signed = false, ...props }: MoneyInputProps) {
  return (
    <Input
      numeric
      leading={currencySymbol(currency || displayCurrency())}
      inputMode={signed ? undefined : 'decimal'}
      {...props}
    />
  )
}

export function Textarea({ className, ...props }: ComponentProps<'textarea'>) {
  const field = useFieldControl()
  return <textarea className={clsx('input', className)} {...field} {...props} />
}
