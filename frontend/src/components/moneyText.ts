import { useCallback } from 'react'

import { usePrivacy } from '@/contexts/privacy'
import { formatMoney, type MoneyFormatter } from '@/lib/money'

/**
 * Privacy mode replaces the digits rather than blurring them: a blurred value
 * is still in the DOM, selectable, copied and read aloud. The dots keep the
 * text's length, so a column keeps its width.
 */
export function maskMoney(text: string): string {
  return text.replace(/\S/g, '•')
}

/**
 * `formatMoney` for an amount rendered as text rather than as `<Money>`: a
 * sentence, a label, a toast, an aria-label. Masked under privacy mode.
 */
export function useMoneyText(): MoneyFormatter {
  const { hidden } = usePrivacy()
  return useCallback(
    (value, options) => {
      const text = formatMoney(value, options)
      return hidden ? maskMoney(text) : text
    },
    [hidden],
  )
}
