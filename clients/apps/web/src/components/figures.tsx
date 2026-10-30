/**
 * The three derived figures on every screen, built so a number we do not have
 * is a dash: a percentage whose window opened at zero (`calculations.md` §4,
 * §11), and an amount the engine could not compute, such as a cost basis with
 * a missing lot (§10). Both are `null` end to end, with no path to `0`.
 */

import { clsx } from 'clsx'
import { TrendingDown, TrendingUp } from 'lucide-react'
import type { ReactNode } from 'react'

import { Money } from '@/components/Money'
import { maskMoney } from '@/components/moneyText'
import { usePrivacy } from '@/contexts/privacy'
import { EM_DASH, formatPercentUnits, parseRate } from '@/lib/format'
import { displayCurrency, type Money as MoneyValue } from '@/lib/money'
import { displayLocale } from '@/lib/locale'

/** An amount, or a dash when there is not one; `title` names the absence. */
export function MoneyOrDash({
  value,
  reason = 'Not available',
  ...rest
}: {
  value: MoneyValue | null | undefined
  reason?: string
  signs?: 'stored' | 'spend' | 'absolute'
  showPlus?: boolean
  showCents?: boolean
  tone?: 'signed' | 'neutral'
  className?: string
}) {
  if (value === null || value === undefined) {
    return (
      <span className="money money--absent" title={reason}>
        {EM_DASH}
      </span>
    )
  }
  return <Money value={value} {...rest} />
}

/** A percentage, or a dash. The only way to render one on these screens. */
export function PercentText({
  rate,
  digits = 2,
  showPlus = false,
  tone = 'signed',
  className,
}: {
  /**
   * Percent units, as every `*_pct` field on the wire carries them: `21.73` is
   * 21.73%. Strings are accepted because a rate is a `Decimal` server-side.
   */
  rate: string | number | null | undefined
  digits?: number
  showPlus?: boolean
  tone?: 'signed' | 'neutral'
  className?: string
}) {
  const percent = parseRate(rate)
  const sign = percent === null || tone === 'neutral' ? '' : percent > 0 ? 'money--in' : percent < 0 ? 'money--out' : ''
  return (
    <span className={clsx('money', sign, percent === null && 'money--absent', className)}>
      {formatPercentUnits(percent, { digits, showPlus })}
    </span>
  )
}

/**
 * "+$60,000.00 (20.00%) 6 month change", the headline delta. When the window
 * opened at zero the percentage is a dash and the arrow is dropped.
 */
export function ChangeBadge({
  amount,
  rate,
  caption,
  size = 'md',
}: {
  amount: MoneyValue
  rate: string | number | null | undefined
  caption?: ReactNode
  size?: 'sm' | 'md'
}) {
  const percent = parseRate(rate)
  const direction = amount > 0 ? 'up' : amount < 0 ? 'down' : 'flat'
  const Icon = direction === 'down' ? TrendingDown : TrendingUp

  return (
    <span className={clsx('change', size === 'sm' && 'change--sm', `change--${direction}`)}>
      {direction === 'flat' ? null : <Icon size={size === 'sm' ? 12 : 14} aria-hidden="true" />}
      <Money value={amount} showPlus tone="signed" />
      <span className="change__pct">({formatPercentUnits(percent, { digits: 2 })})</span>
      {caption ? <span className="change__caption">{caption}</span> : null}
    </span>
  )
}

/** One labelled figure in a `.stat-row`: the value line holds the figure and any `.stat__aside`. */
export function Stat({
  label,
  sub,
  children,
}: {
  label: ReactNode
  /** A muted line under the figure. */
  sub?: ReactNode
  children: ReactNode
}) {
  return (
    <div className="stat">
      <p className="stat__label">{label}</p>
      <p className="stat__value figure--stat">{children}</p>
      {sub ? <p className="muted">{sub}</p> : null}
    </div>
  )
}

/**
 * A quoted price: a dollar value that is not `Money` (a `Rate` in the core,
 * more than two places, never summed). It still masks under privacy mode.
 */
export function PriceText({
  price,
  currency,
  digits = 4,
  className,
}: {
  price: string | number | null | undefined
  /**
   * The security's own currency. Falls back to the space's currency, never to
   * a hardcoded USD.
   */
  currency?: string
  digits?: number
  className?: string
}) {
  const { hidden } = usePrivacy()
  const parsed = parseRate(price)

  if (parsed === null) {
    return (
      <span className={clsx('money money--absent', className)} title="No quote on file yet.">
        {EM_DASH}
      </span>
    )
  }

  const text = new Intl.NumberFormat(displayLocale(), {
    style: 'currency',
    currency: currency || displayCurrency(),
    maximumFractionDigits: digits,
  }).format(parsed)

  return (
    <span
      className={clsx('money', hidden && 'money--hidden', className)}
      data-private={hidden ? 'true' : undefined}
    >
      {hidden ? maskMoney(text) : text}
    </span>
  )
}
