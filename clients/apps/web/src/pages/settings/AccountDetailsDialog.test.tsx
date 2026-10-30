/** A property with no valuation source: the dialog takes the token itself. */

import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { VALUATION_SOURCES_KEY } from '@/lib/clients/connections'
import { formatDate } from '@/lib/format'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { account } from '@/test/builders'
import { dialogSubmit } from '@/test/dialogStub'
import { renderScreen } from '@/test/renderScreen'

import { AccountDetailsDialog } from './AccountDetailsDialog'

const HOUSE = account({
  id: 'house-1',
  name: 'Maple Street',
  kind: 'asset',
  type: 'real_estate',
  history: { starts_on: '2024-03-01', automatic_starts_on: '2024-03-01' },
})

function render(configured: string[], account: AccountWithBalances = HOUSE): string {
  return renderScreen(<AccountDetailsDialog account={account} open onOpenChange={() => {}} />, {
    seed: [[VALUATION_SOURCES_KEY, { asset_types: ['real_estate', 'vehicle'], configured }]],
  })
}

describe('AccountDetailsDialog valuation', () => {
  it('points to the server admin, and takes no credential, when nothing can price the house', () => {
    const html = render([])
    expect(html).toContain('once the server admin configures the Camoufox browser')
    expect(html).not.toContain('type="password"')
  })

  it('offers an estimate once a source is configured', () => {
    const html = render(['real_estate'])
    expect(html).not.toContain('Camoufox browser')
    expect(html).toContain('Get an estimate')
  })
})

describe('AccountDetailsDialog history start', () => {
  it('shows the automatic start and offers no reset while nothing is typed', () => {
    const html = render([])
    expect(html).toContain('History starts')
    expect(html).toContain(`Automatic: ${formatDate('2024-03-01')}`)
    expect(html).not.toContain('Use automatic')
  })

  it('shows the override with the automatic day beside it and a way back', () => {
    const html = render([], {
      ...HOUSE,
      history_starts_on: '2020-06-01',
      history: { starts_on: '2020-06-01', automatic_starts_on: '2024-03-01' },
    })
    expect(html).toContain('value="2020-06-01"')
    expect(html).toContain(`Set by hand (automatic: ${formatDate('2024-03-01')})`)
    expect(html).toContain('Use automatic')
  })
})

describe('AccountDetailsDialog sections', () => {
  it('holds closing the account, which the account menu does not offer', () => {
    expect(render([])).toContain('Closed')
  })

  it('offers to make a synced account manual, and says nothing of it for a manual one', () => {
    expect(render([])).not.toContain('Make manual')
    const html = render([], { ...HOUSE, connection_id: 'conn-1' })
    expect(html).toContain('Synced through SimpleFIN')
    expect(html).toContain('Make manual')
  })

  it('folds the rarely changed settings under Advanced', () => {
    const html = render([])
    const fold = html.indexOf('<summary>Advanced</summary>')
    expect(fold).toBeGreaterThan(-1)
    expect(html.indexOf('Currency')).toBeGreaterThan(fold)
    expect(html.indexOf('Opening balance')).toBeGreaterThan(fold)
    expect(html.indexOf('Name')).toBeLessThan(fold)
  })
})

describe('AccountDetailsDialog across accounts', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    dialogSubmit.current = undefined
  })

  const CAR_B = {
    ...HOUSE,
    id: 'car-b',
    name: 'Green Wagon',
    type: 'vehicle',
    vehicle_vin: 'EXAMPLEVIN0000002',
    vehicle_mileage: 73000,
  }

  // A static render keeps no state between renders, so what the key does
  // across a rerender is covered in lib/accountDraft.test.ts; this is the
  // form's own seeding and its save.
  it("shows the account it is opened for, and saves nothing of another's", async () => {
    const html = render(['vehicle'], CAR_B)
    expect(html).toContain('Green Wagon')
    expect(html).toContain('value="EXAMPLEVIN0000002"')
    expect(html).toContain('value="73000"')

    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => null },
      text: () => Promise.resolve('{}'),
    })
    vi.stubGlobal('fetch', fetcher)
    dialogSubmit.current?.()
    await vi.waitFor(() => expect(fetcher).toHaveBeenCalled())
    const [url, init] = fetcher.mock.calls[0]
    expect(url).toBe('/api/accounts/car-b')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(init.body)).toEqual({})
  })
})
