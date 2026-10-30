import { clsx } from 'clsx'

import { maskMoney } from '@/components/moneyText'
import { usePrivacy } from '@/contexts/privacy'
import {
  ZERO_MONEY,
  formatMoney,
  type CurrencyAmount,
  type Money as MoneyValue,
  type SignConvention,
} from '@/lib/money'

export interface MoneyProps {
  value: MoneyValue
  /**
   * Which sign to *print*. Storage has exactly one convention — expenses
   * negative — and the screens have three: the register prints spend positive,
   * a balance prints a debt negative, a delta prints an explicit `+`.
   */
  signs?: SignConvention
  showPlus?: boolean
  showCents?: boolean
  /**
   * `false` drops the currency symbol, for the register where every row shares
   * one. Never where two currencies can appear together.
   */
  showSymbol?: boolean
  currency?: string
  locale?: string
  /**
   * `signed` colours by the stored sign; `neutral` leaves body text. Balances
   * are neutral: a negative card balance is a debt, not money going out.
   * `flow` colours income only, keeping red for figures where negative is a
   * warning.
   */
  tone?: 'signed' | 'flow' | 'neutral'
  className?: string
}

/**
 * The one component that renders an amount. Colour comes from the *stored*
 * sign, never the printed one, or a `spend` convention would turn every
 * expense into income.
 */
export function Money({
  value,
  signs = 'stored',
  showPlus = false,
  showCents = true,
  showSymbol = true,
  currency,
  locale,
  tone = 'signed',
  className,
}: MoneyProps) {
  const { hidden } = usePrivacy()
  const text = formatMoney(value, { signs, showPlus, showCents, showSymbol, currency, locale })

  return (
    <span
      className={clsx('money', toneClass(tone, value), hidden && 'money--hidden', className)}
      data-private={hidden ? 'true' : undefined}
    >
      {hidden ? maskMoney(text) : text}
    </span>
  )
}

/**
 * A total that may span currencies: one figure per currency, never a sum
 * across them. Nothing to show reads as a zero in the display currency.
 */
export function MoneyTotals({
  totals,
  ...rest
}: Omit<MoneyProps, 'value' | 'currency'> & { totals: readonly CurrencyAmount[] }) {
  if (totals.length === 0) return <Money value={ZERO_MONEY} {...rest} />
  if (totals.length === 1) {
    return <Money value={totals[0].amount} currency={totals[0].currency} {...rest} />
  }
  return (
    <span className="money-totals">
      {totals.map((total, index) => (
        <span key={total.currency}>
          {index > 0 ? ' + ' : null}
          <Money value={total.amount} currency={total.currency} {...rest} />
        </span>
      ))}
    </span>
  )
}

function toneClass(tone: MoneyProps['tone'], value: MoneyValue): string | false {
  if (tone === 'signed') return signClass(value)
  // A flow colours only what came in. Everything else — a spend, a zero —
  // takes the body colour, which is what `money--zero` already is.
  if (tone === 'flow') return value > 0 ? 'money--in' : 'money--zero'
  return false
}

function signClass(value: MoneyValue): string {
  if (value > 0) return 'money--in'
  if (value < 0) return 'money--out'
  return 'money--zero'
}
