import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { AllocationSlice } from '@/lib/clients/investments'
import { parseMoney } from '@/lib/money'
import { AllocationCard } from './AllocationCard'

/** The tail is the point: many holdings under 1% fold into one row. */
function slice(symbol: string, share: string, value: string): AllocationSlice {
  return { security_id: symbol, symbol, share, value: parseMoney(value) } as AllocationSlice
}

const PORTFOLIO = [
  slice('NWGRX', '0.310', '61400.00'),
  slice('NW60', '0.118', '23370.00'),
  slice('ZZZA', '0.0004', '30.00'),
  slice('ZZZB', '0.00001', '7.50'),
]

describe('the allocation card', () => {
  it('lists what clears one per cent and folds what does not', () => {
    const markup = renderToStaticMarkup(<AllocationCard allocation={PORTFOLIO} />)
    expect(markup).toContain('NWGRX')
    expect(markup).toContain('NW60')
    expect(markup).not.toContain('ZZZA')
    expect(markup).not.toContain('ZZZB')
  })

  it('counts and totals what it folded rather than dropping it', () => {
    const markup = renderToStaticMarkup(<AllocationCard allocation={PORTFOLIO} />)
    expect(markup).toContain('2 holdings under 1%')
    expect(markup).toContain('37.50')
  })

  it('offers no fold when every holding earns its row', () => {
    const markup = renderToStaticMarkup(<AllocationCard allocation={PORTFOLIO.slice(0, 2)} />)
    expect(markup).not.toContain('under 1%')
  })
})
