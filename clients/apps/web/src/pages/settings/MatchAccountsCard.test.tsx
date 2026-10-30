import { describe, expect, it } from 'vitest'

import type { Connection, LinkCandidates } from '@/lib/clients/connections'
import { parseMoney } from '@/lib/money'
import { renderScreen } from '@/test/renderScreen'

import { MatchAccountsCard } from './MatchAccountsCard'

const CONNECTION: Connection = {
  id: 'conn-1',
  name: 'Household bridge',
  status: 'pending_link',
  status_detail: null,
  needs_setup_token: false,
  bank_warnings: [],
  ignored: [],
  last_sync_at: null,
  last_successful_sync_at: null,
  retry_not_before: null,
  created_at: '2026-01-01T00:00:00Z',
  sync: null,
}

const CANDIDATES: LinkCandidates = {
  remote: [
    {
      external_id: 'b-1',
      name: 'CHECKING',
      kind: 'cash',
      institution: 'Big Bank',
      masked_number: '0000',
      balance: parseMoney('250.00'),
      currency: 'USD',
      linked_account_id: null,
      suggested: ['local-1'],
      likely: 'local-1',
      match: 'number',
    },
    {
      external_id: 'b-2',
      name: 'Dust Wallet (0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9)',
      kind: 'investment',
      institution: 'Example Exchange',
      masked_number: '',
      balance: parseMoney('0.12'),
      currency: 'USD',
      linked_account_id: null,
      suggested: ['local-2'],
      likely: null,
      match: 'weak',
    },
  ],
  local: [
    {
      id: 'local-1',
      name: 'Everyday Checking',
      kind: 'cash',
      masked_number: '0000',
      balance: parseMoney('250.00'),
      sync_floor_on: '2026-08-15',
      linked_to: '',
    },
    {
      id: 'local-2',
      name: 'Brokerage',
      kind: 'investment',
      masked_number: '',
      balance: parseMoney('900.00'),
      sync_floor_on: null,
      linked_to: '',
    },
  ],
  ignored: [],
}

function render(): string {
  return renderScreen(<MatchAccountsCard connection={CONNECTION} />, {
    seed: [[['connections', CONNECTION.id, 'candidates'], CANDIDATES]],
  })
}

describe('the match screen', () => {
  it('says why it chose the likely match', () => {
    // The choice itself is pairing()'s; a closed select renders no value here.
    expect(render()).toContain('Likely match: same account number')
  })

  it('leaves a weak guess unchosen, says so, and names the account without its feed id', () => {
    const markup = render()
    expect(markup).toContain('Dust Wallet')
    expect(markup).not.toContain('0a1b2c3d')
    expect(markup).toContain('No clear match: only the type, bank or balance is alike')
  })

  it('offers to select every account and the small balances', () => {
    const markup = render()
    expect(markup).toContain('aria-label="Select every account"')
    expect(markup).toContain('Select balances under')
    expect(markup).toContain('Do not import')
  })
})
