/**
 * Connecting an eBay account on eBay's own page. The account and the consent
 * address are invented.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// stubbed to render in place.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { merchantKey, type MerchantAccount } from '@/lib/clients/merchant'
import type { Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { ConnectEbayDialog } from './ConnectEbayDialog'

const ACCOUNT = { id: 'acct-1' as Uuid, name: 'eBay (Alex)' } as MerchantAccount

function render(consent: { url: string; sandbox: boolean }): string {
  return renderScreen(
    <ConnectEbayDialog merchant="ebay" account={ACCOUNT} onConnected={() => {}} onClose={() => {}} />,
    { seed: [[[...merchantKey('ebay'), 'consent', ACCOUNT.id], consent]] },
  )
}

describe('connecting an eBay account', () => {
  it('sends the person to eBay and asks for the address eBay lands them on', () => {
    const html = render({ url: 'https://auth.ebay.com/oauth2/authorize?client_id=x', sandbox: false })
    expect(html).toContain('Open eBay')
    expect(html).toContain('The address eBay showed')
    expect(html).not.toContain('sandbox')
  })

  it("says so when the server uses eBay's sandbox", () => {
    const html = render({ url: 'https://auth.sandbox.ebay.com/oauth2/authorize?client_id=x', sandbox: true })
    expect(html).toContain('sandbox test user')
  })
})
