/**
 * A number we do not have is a dash, at the glyph: a null `change_pct` from
 * `/net-worth` must survive every component to the screen. `calculations.md`
 * §4 and §11: a percentage against a zero start renders "—", never "∞" or "0%".
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { changeFraction } from '@/lib/format'
import { moneyFromCents, ZERO_MONEY } from '@/lib/money'

import { ChangeBadge, MoneyOrDash, PercentText } from './figures'

function text(node: Parameters<typeof renderToStaticMarkup>[0]): string {
  return renderToStaticMarkup(node).replace(/<[^>]*>/g, ' ')
}

describe('a percentage against a zero start', () => {
  it('renders an em dash', () => {
    expect(text(<PercentText rate={null} />)).toContain('—')
  })

  it('never renders infinity or zero', () => {
    const rendered = text(<PercentText rate={null} showPlus />)
    expect(rendered).not.toContain('∞')
    expect(rendered).not.toContain('Infinity')
    expect(rendered).not.toContain('0.00%')
  })

  /**
   * The end-to-end case: an account opened *inside* the window. Its start
   * balance is zero, so `change_pct` comes back null, and the badge beside its
   * balance has to say so.
   */
  it('reaches the change badge beside a balance', () => {
    const start = ZERO_MONEY
    const end = moneyFromCents(120_000)
    const rate = changeFraction(start, end)

    expect(rate).toBeNull()
    const rendered = text(<ChangeBadge amount={end} rate={rate} caption="6 month change" />)
    expect(rendered).toContain('—')
    expect(rendered).not.toContain('0.00%')
    // The amount is still known and still printed; only the ratio is missing.
    expect(rendered).toContain('+$1,200.00')
  })

  it('is not the same as a real zero move, which does print 0.00%', () => {
    const rendered = text(<PercentText rate={0} />)
    expect(rendered).toContain('0.00%')
    expect(rendered).not.toContain('—')
  })
})

describe('an amount we do not have', () => {
  it('renders a dash with the reason attached, rather than zero', () => {
    const rendered = renderToStaticMarkup(
      <MoneyOrDash value={null} reason="Cost basis is incomplete." />,
    )
    expect(rendered).toContain('—')
    expect(rendered).toContain('Cost basis is incomplete.')
    expect(rendered).not.toContain('$0.00')
  })

  it('prints a real zero as a figure, because that is a different fact', () => {
    expect(text(<MoneyOrDash value={ZERO_MONEY} />)).toContain('$0.00')
  })
})

describe('the unit a rate arrives in', () => {
  /**
   * `internal/domain.Percent` multiplies by a hundred before serializing, so
   * every `*_pct` field is already in percent units.
   */
  it('is percent units, the way every _pct field carries it', () => {
    expect(text(<PercentText rate="4.21" />)).toContain('4.21%')
    expect(text(<PercentText rate={-1190.05} />)).toContain('-1190.05%')
  })

  it('reads the same through the change badge', () => {
    const rendered = text(
      <ChangeBadge amount={moneyFromCents(2_200_000)} rate="4.21" caption="6 month change" />,
    )
    expect(rendered).toContain('4.21%')
    expect(rendered).not.toContain('421.00%')
  })

  it('still tells a real zero from an absent one', () => {
    expect(text(<PercentText rate={0} />)).toContain('0.00%')
    expect(text(<PercentText rate={null} />)).toContain('—')
  })
})
