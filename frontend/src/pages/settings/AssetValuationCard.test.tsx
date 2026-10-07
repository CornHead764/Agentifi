import { describe, expect, it } from 'vitest'

import { VALUATION_SOURCES_KEY } from '@/lib/clients/connections'
import { renderScreen } from '@/test/renderScreen'

import { AssetValuationCard } from './AssetValuationCard'

function render(configured: string[]): string {
  return renderScreen(<AssetValuationCard />, {
    seed: [[VALUATION_SOURCES_KEY, { asset_types: ['real_estate', 'vehicle'], configured }]],
  })
}

describe('the household asset-valuation card', () => {
  it('points to the server admin and takes no credential', () => {
    const html = render([])
    expect(html).toContain('once the server admin configures the Camoufox browser')
    expect(html).not.toContain('type="password"')
    expect(html).not.toContain('Re-price all now')
  })

  it('re-prices on demand once the server has a source', () => {
    const html = render(['real_estate', 'vehicle'])
    expect(html).toContain('is live.')
    expect(html).toContain('Re-price all now')
    expect(html).not.toContain('once the server admin configures')
  })
})
