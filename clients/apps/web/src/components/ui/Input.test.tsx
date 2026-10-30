import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it } from 'vitest'

import { setDisplayCurrency } from '@/lib/money'

import { MoneyInput } from './Input'

describe('the money input', () => {
  afterEach(() => setDisplayCurrency('USD'))

  it('leads with the household currency, not a hard-coded dollar sign', () => {
    setDisplayCurrency('EUR')
    const markup = renderToStaticMarkup(<MoneyInput />)
    expect(markup).toContain('€')
    expect(markup).not.toContain('$')
  })

  it('leads with an amount’s own currency when it has one', () => {
    expect(renderToStaticMarkup(<MoneyInput currency="GBP" />)).toContain('£')
  })

  it('offers the decimal keypad only to an amount that cannot be negative', () => {
    expect(renderToStaticMarkup(<MoneyInput />)).toContain('inputMode="decimal"')
    expect(renderToStaticMarkup(<MoneyInput signed />)).not.toContain('inputMode')
  })
})
