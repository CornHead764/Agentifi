import { describe, expect, it, vi } from 'vitest'

import { ACCOUNTS_KEY } from '@/lib/transactions/cache'
import { effectiveKind } from '@/lib/accountTypes'
import { parseMoney, ZERO_MONEY } from '@/lib/money'
import {
  emptyByInstitution,
  IGNORED_ACCOUNTS_KEY,
  type IgnoredLocalAccount,
} from '@/lib/clients/ignoredAccounts'
import type { ConnectionList, SyncRun } from '@/lib/clients/connections'
import { INSTITUTIONS_KEY, type Institution } from '@/lib/clients/institutions'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { account } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'
import { AccountDetailsDialog } from './AccountDetailsDialog'
import { AccountMenu, AccountsSettings, HeldBalanceAlert } from './AccountsSettings'

/** The page renders with every query pending, the state it is first seen in. */
function render() {
  return renderScreen(<AccountsSettings />)
}

describe('the accounts settings page', () => {
  it('renders with no server behind it', () => {
    const markup = render()
    expect(markup).toContain('Connections')
    expect(markup).toContain('Manual accounts')
  })

  it('offers a way to add a manual account regardless of SimpleFIN', () => {
    // The connector is off by default in this render, and that is exactly the
    // deployment shape a hand-kept account has to work for.
    expect(render()).toContain('Add an account by hand')
  })

  it('offers no connect form until the server says the connector is on', () => {
    // Off by default. A form that always refuses is worse than no form.
    expect(render()).not.toContain('Connect an institution')
  })
})

describe("a connection's sync", () => {
  const run: SyncRun = {
    state: 'running',
    phase: 'importing',
    account: 2,
    accounts: 5,
    account_name: 'Joint Savings',
    transactions_imported: 40,
    transactions_updated: 0,
    accounts_created: 0,
    warnings: 0,
    balances_held: 0,
    message: null,
    started_at: '2026-03-20T12:00:00Z',
    finished_at: null,
  }

  function withSync(sync: SyncRun) {
    const listing: ConnectionList = {
      simplefin_enabled: true,
      schedule: { enabled: false, at: '04:00', time_zone: 'UTC', next_run_at: null },
      connections: [
        {
          id: 'conn-1',
          name: 'Household bridge',
          status: 'active',
          status_detail: null,
          needs_setup_token: false,
          bank_warnings: [],
          ignored: [],
          last_sync_at: null,
          last_successful_sync_at: null,
          retry_not_before: null,
          created_at: '2026-01-01T00:00:00Z',
          sync,
        },
      ],
    }
    return renderScreen(<AccountsSettings />, { seed: [[['connections'], listing]] })
  }

  it('shows how far a running sync has got', () => {
    const markup = withSync(run)
    expect(markup).toContain('Importing account 2 of 5: Joint Savings')
    expect(markup).toContain('Syncing…')
  })

  it('says why the last sync did not finish and offers another', () => {
    const markup = withSync({
      ...run,
      state: 'failed',
      phase: null,
      message: 'the Bridge did not answer',
      finished_at: '2026-03-20T12:02:00Z',
    })
    expect(markup).toContain('The last sync did not finish')
    expect(markup).toContain('The Bridge did not answer.')
    expect(markup).toContain('Try again')
  })
})

describe('the account actions menu', () => {
  it('mounts an actions trigger for a manual account without crashing', () => {
    // Radix portals the dropdown's items to document.body, which this static
    // harness lacks; the trigger's aria-label proves the row mounts its menu.
    const markup = renderScreen(<AccountsSettings />, {
      seed: [[ACCOUNTS_KEY, [account({ id: 'acct-1', name: 'Old Savings' })]]],
    })
    expect(markup).toContain('Actions for Old Savings')
  })

  it('lists an account hidden for its small balance behind a line that says so', () => {
    const markup = renderScreen(<AccountsSettings />, {
      seed: [
        [
          ACCOUNTS_KEY,
          [
            account({ id: 'acct-1', name: 'Kept Checking' }),
            account({ id: 'acct-2', name: 'Dust Wallet', hidden_small_balance: true }),
          ],
        ],
      ],
    })
    expect(markup).toContain('Kept Checking')
    expect(markup).not.toContain('Dust Wallet')
    expect(markup).toContain('1 small balance hidden')
  })

  it('lists the ignored accounts and offers to ignore an institution\'s empty ones', () => {
    const institutions: Institution[] = [
      { id: 'inst-x', name: 'Example Exchange', logo_url: null, hide_below_balance: null },
      { id: 'inst-b', name: 'Example Bank', logo_url: null, hide_below_balance: null },
    ]
    const ignored: IgnoredLocalAccount[] = [
      {
        id: 'acct-9',
        name: 'Old Coin Wallet',
        type: 'crypto',
        kind: 'investment',
        masked_number: null,
        connection_id: null,
        institution_id: 'inst-x',
        institution: 'Example Exchange',
        balance: ZERO_MONEY,
        ignored_at: '2026-09-01T12:00:00Z',
      },
    ]
    const accounts = [
      account({ id: 'acct-1', name: 'Coin A', institution_id: 'inst-x' }),
      account({ id: 'acct-2', name: 'Coin B', institution_id: 'inst-x' }),
      account({ id: 'acct-3', name: 'Everyday Checking', institution_id: 'inst-b',
        balances: { ...account({}).balances, balance: parseMoney('120.00') } }),
    ]
    // The card starts folded; this is the browser that opened it.
    vi.stubGlobal('window', { localStorage: { getItem: () => '0' } })
    const markup = renderScreen(<AccountsSettings />, {
      seed: [
        [INSTITUTIONS_KEY, institutions],
        [IGNORED_ACCOUNTS_KEY, ignored],
        [ACCOUNTS_KEY, accounts],
      ],
    })
    vi.unstubAllGlobals()
    expect(markup).toContain('Ignored accounts')
    expect(markup).toContain('Old Coin Wallet')
    expect(markup).toContain('Stop ignoring')
    expect(markup).toContain('Accounts holding nothing')
    expect(markup).toContain('2 accounts at zero')
    expect(markup).not.toContain('Example Bank')
  })

  it('renders the delete confirmation without crashing, manual or SimpleFIN-linked', () => {
    const renderMenu = (connectionId: string | null) => {
      return renderScreen(
        <AccountMenu
          account={account({ id: 'acct-2', name: 'Checking', connection_id: connectionId })}
        />,
      )
    }
    expect(() => renderMenu(null)).not.toThrow()
    expect(() => renderMenu('connection-1')).not.toThrow()
  })
})

/** An account with only the fields a test actually reads set to something real. */
/**
 * The dialog renders with an account whose `type` is not on the picker's list.
 * Radix portals its body away under this harness, so this guards only against
 * a crash.
 */
function rendersWithoutCrashing(one: AccountWithBalances): void {
  renderScreen(<AccountDetailsDialog account={one} open onOpenChange={() => {}} />)
}

describe('the "Secured on" field', () => {
  it('shows for a loan-kind account whose type the picker does not offer', () => {
    // A stored `kind` of loan must not depend on the picker recognising the
    // stored `type`, or an imported loan can never be linked to its asset.
    const one = account({ kind: 'loan', type: 'reverse_mortgage' })
    expect(effectiveKind(one.type, one)).toBe('loan')
    rendersWithoutCrashing(one)
  })

  it('still shows for a loan type the picker does offer', () => {
    const one = account({ kind: 'loan', type: 'mortgage' })
    expect(effectiveKind(one.type, one)).toBe('loan')
  })

  it('stays hidden once the type is switched to a known non-loan type', () => {
    // The picker's own answer for the chosen type wins the moment it has one
    // — the stored kind is only a fallback for a type the picker cannot read.
    const one = account({ kind: 'loan', type: 'reverse_mortgage' })
    expect(effectiveKind('checking', one)).toBe('cash')
  })

  it('stays hidden for a non-loan account of an unrecognised type', () => {
    const one = account({ kind: 'cash', type: 'some_new_bank_product' })
    expect(effectiveKind(one.type, one)).toBe('cash')
  })
})

describe('the empty-accounts offer', () => {
  it('counts the zero balances per institution, most first, and skips unknown banks', () => {
    const names = new Map([
      ['inst-a', 'Alpha Exchange'],
      ['inst-b', 'Beta Bank'],
    ])
    const offers = emptyByInstitution(
      [
        account({ id: '1', institution_id: 'inst-b' }),
        account({ id: '2', institution_id: 'inst-a' }),
        account({ id: '3', institution_id: 'inst-a' }),
        account({
          id: '4',
          institution_id: 'inst-a',
          balances: { ...account({}).balances, balance: parseMoney('0.01') },
        }),
        account({ id: '5', institution_id: 'inst-gone' }),
        account({ id: '6', institution_id: null }),
      ],
      names,
    )
    expect(offers).toEqual([
      { id: 'inst-a', name: 'Alpha Exchange', count: 2 },
      { id: 'inst-b', name: 'Beta Bank', count: 1 },
    ])
  })
})

describe('a held balance', () => {
  it('says what the reported figure was compared with', () => {
    const reason =
      'SimpleFIN reported $0.00; the last balance -$1,234.56 plus 1 new transaction (+$1,000.00) ' +
      'comes to -$234.56. Keeping -$234.56 until you confirm the change.'
    const markup = renderScreen(
      <HeldBalanceAlert
        account={account({
          name: 'Travel Card',
          provider_balance: parseMoney('-234.56'),
          withheld_balance: ZERO_MONEY,
          withheld_balance_at: '2026-03-20T00:00:00Z',
          withheld_balance_reason: reason,
        })}
      />,
    )
    expect(markup).toContain('Travel Card now reports')
    expect(markup).toContain(reason)
  })
})
