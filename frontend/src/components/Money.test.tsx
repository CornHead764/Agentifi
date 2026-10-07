import type { ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { PrivacyContext, type PrivacyValue } from '@/contexts/privacy'
import { ZERO_MONEY, moneyFromCents, type Money as MoneyValue } from '@/lib/money'

import { Money } from './Money'
import { useMoneyText } from './moneyText'

const PRIVATE: PrivacyValue = { hidden: true, setHidden: () => {}, toggle: () => {} }

function render(node: ReactNode): string {
  return renderToStaticMarkup(node)
}

const GROCERIES = moneyFromCents(-41_200)
const PAYCHECK = moneyFromCents(190_000)

describe('<Money>', () => {
  it('prints the stored sign by default', () => {
    expect(render(<Money value={GROCERIES} />)).toContain('-$412.00')
    expect(render(<Money value={PAYCHECK} />)).toContain('$1,900.00')
  })

  it('flips the sign for spend conventions without changing the colour', () => {
    const html = render(<Money value={GROCERIES} signs="spend" />)

    // The figure reads positive, as a spending-plan or category row shows it…
    expect(html).toContain('$412.00')
    expect(html).not.toContain('-$412.00')
    // …but it is still money leaving, so it is still coral.
    expect(html).toContain('money--out')
    expect(html).not.toContain('money--in')
  })

  it('colours income green, expense coral and zero neither', () => {
    expect(render(<Money value={PAYCHECK} />)).toContain('money--in')
    expect(render(<Money value={GROCERIES} />)).toContain('money--out')

    const zero = render(<Money value={ZERO_MONEY} />)
    expect(zero).toContain('money--zero')
    expect(zero).not.toContain('money--in')
    expect(zero).not.toContain('money--out')
  })

  // Red is kept for figures where negative is a warning; money arriving is
  // still marked.
  it('colours only the income in a flow, leaving the spending in body text', () => {
    const spend = render(<Money value={GROCERIES} tone="flow" />)
    expect(spend).toContain('-$412.00')
    expect(spend).not.toContain('money--out')

    expect(render(<Money value={PAYCHECK} tone="flow" />)).toContain('money--in')
    expect(render(<Money value={ZERO_MONEY} tone="flow" />)).not.toContain('money--out')
  })

  // The colour comes off the stored sign, not the printed one.
  it('reads a flow off the stored sign even where the figure prints positive', () => {
    const html = render(<Money value={GROCERIES} tone="flow" signs="spend" />)

    expect(html).toContain('$412.00')
    expect(html).not.toContain('money--in')
  })

  it('leaves balances uncoloured when asked, because a debt is not a spend', () => {
    const html = render(<Money value={moneyFromCents(-20_000_000)} tone="neutral" />)

    expect(html).toContain('-$200,000.00')
    expect(html).not.toContain('money--out')
  })

  it('never prints a negative zero', () => {
    expect(render(<Money value={ZERO_MONEY} signs="spend" />)).toContain('$0.00')
    expect(render(<Money value={ZERO_MONEY} signs="spend" />)).not.toContain('-$0.00')
  })

  it('marks a positive delta with a plus', () => {
    expect(render(<Money value={PAYCHECK} showPlus />)).toContain('+$1,900.00')
  })

  describe('privacy mode', () => {
    const hide = (node: ReactNode) =>
      render(<PrivacyContext value={PRIVATE}>{node}</PrivacyContext>)

    it('keeps the digits out of the DOM entirely', () => {
      const html = hide(<Money value={GROCERIES} />)

      expect(html).not.toContain('412')
      expect(html).not.toMatch(/\d/)
      expect(html).toContain('money--hidden')
      expect(html).toContain('data-private="true"')
    })

    it('masks one character per character, so the column does not reflow', () => {
      const shown = render(<Money value={PAYCHECK} />)
      const hidden = hide(<Money value={PAYCHECK} />)

      expect(text(hidden)).toHaveLength(text(shown).length)
      expect(text(hidden)).toBe('•'.repeat(text(shown).length))
    })

    it('hides nothing when no provider is mounted', () => {
      expect(render(<Money value={PAYCHECK} />)).not.toContain('money--hidden')
    })
  })
})

describe('useMoneyText', () => {
  function Sentence({ value }: { value: MoneyValue }) {
    const moneyText = useMoneyText()
    return <p>{`Saved: ${moneyText(value, { signs: 'absolute' })}.`}</p>
  }

  it('formats as formatMoney does while amounts are shown', () => {
    expect(text(render(<Sentence value={GROCERIES} />))).toBe('Saved: $412.00.')
  })

  it('masks the amount and nothing else under privacy mode, as <Money> does', () => {
    const html = render(
      <PrivacyContext value={PRIVATE}>
        <Sentence value={GROCERIES} />
        <Money value={GROCERIES} signs="absolute" />
      </PrivacyContext>,
    )

    expect(html).not.toMatch(/\d/)
    expect(html).toContain('Saved: •••••••.')
    expect(html).toContain('>•••••••</span>')
  })
})

/** The text between the outermost tags of a single-element render. */
function text(html: string): string {
  return html.replace(/<[^>]*>/g, '')
}
