import type { QueryClient } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { parseMoney } from '@/lib/money'
import { testQueryClient } from '@/test/renderScreen'

import {
  challengeInstructions,
  describeAutopay,
  describePullResult,
  describeBillHistory,
  describePullStatus,
  forgetBillConnection,
  getSeriesBillLink,
  linkSeriesToBill,
  listBillsSettledBy,
  listBillSubaccounts,
  listBills,
  matchBillHistory,
  unlinkSeriesFromBill,
  type BillAccountHistory,
  type BillPullResult,
} from './bills'

/** One bill as the resource sends it: every figure a string. */
function wireBill(over: Record<string, unknown> = {}) {
  return {
    id: 'b1',
    subaccount_id: 'sub1',
    due_on: '2026-10-26',
    amount_due: '120.00',
    currency: 'USD',
    issued_on: '2026-09-24',
    period_start: '2026-08-24',
    period_end: '2026-09-23',
    autopay_on: null,
    pays_on: '2026-10-21',
    status: 'open',
    source: 'provider',
    statement_url: '',
    document_id: 'doc1',
    fetched_at: '2026-09-25T06:00:00Z',
    amended_at: null,
    ...over,
  }
}

/** One subaccount as the resource sends it, link and all. */
function wireSubaccount(over: Record<string, unknown> = {}) {
  return {
    id: 'sub1',
    connection_id: 'c1',
    biller: 'we-energies',
    external_id: 'premise-1',
    label: 'Main account',
    masked_number: null,
    is_selected: true,
    series_id: null,
    ...over,
  }
}

function respond(body: unknown, status = 200) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    headers: { get: () => null },
    text: () => Promise.resolve(JSON.stringify(body)),
  })
}

function calledWith(fetcher: ReturnType<typeof respond>): {
  url: string
  method: string
  body: unknown
} {
  const [url, init] = fetcher.mock.calls[0] as [string, { method: string; body: unknown }]
  return { url, method: init.method, body: init.body }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('reading bills', () => {
  it('coerces the amount once, at this boundary', async () => {
    const fetcher = respond([wireBill()])
    vi.stubGlobal('fetch', fetcher)

    const bills = await listBills('sub1')

    expect(bills[0].amount_due).toBe(parseMoney('120.00'))
    expect(calledWith(fetcher).url).toBe('/api/bills/subaccounts/sub1/bills')
  })
})

describe('reading what a login bills for', () => {
  it('filters by connection in the query, not in the path', async () => {
    const fetcher = respond([wireSubaccount()])
    vi.stubGlobal('fetch', fetcher)

    await listBillSubaccounts('c1')

    expect(calledWith(fetcher).url).toBe('/api/bills/subaccounts?connection_id=c1')
  })

  it('asks for every one when no connection is named', async () => {
    const fetcher = respond([wireSubaccount()])
    vi.stubGlobal('fetch', fetcher)

    await listBillSubaccounts()

    expect(calledWith(fetcher).url).toBe('/api/bills/subaccounts')
  })
})

describe('the link between a series and a subaccount', () => {
  it('is written with both ids in the body, keyed by the series', async () => {
    const fetcher = respond({
      series_id: 's1',
      subaccount_id: 'sub2',
      created_at: '2026-09-17T10:00:00Z',
    })
    vi.stubGlobal('fetch', fetcher)

    await linkSeriesToBill('s1', 'sub2')

    const call = calledWith(fetcher)
    expect(call.method).toBe('POST')
    expect(call.url).toBe('/api/bills/links')
    expect(JSON.parse(call.body as string)).toEqual({
      series_id: 's1',
      subaccount_id: 'sub2',
    })
  })

  it('is read off the subaccount, which is the only place the resource says it', async () => {
    const fetcher = respond([
      wireSubaccount(),
      wireSubaccount({ id: 'sub2', connection_id: 'c2', series_id: 's1' }),
    ])
    vi.stubGlobal('fetch', fetcher)

    expect(await getSeriesBillLink('s1')).toEqual({
      series_id: 's1',
      subaccount_id: 'sub2',
      connection_id: 'c2',
    })
  })

  it('answers null for a series that follows no bill', async () => {
    vi.stubGlobal('fetch', respond([wireSubaccount()]))

    expect(await getSeriesBillLink('s1')).toBeNull()
  })

  it('is dropped by the series alone', async () => {
    const fetcher = respond(null)
    vi.stubGlobal('fetch', fetcher)

    await unlinkSeriesFromBill('s1')

    const call = calledWith(fetcher)
    expect(call.method).toBe('DELETE')
    expect(call.url).toBe('/api/bills/links/s1')
  })
})

describe('the autopay rule in words', () => {
  const none = { autopay_rule: 'none', autopay_days: null, autopay_day: null } as const

  it('says nothing is scheduled when nothing is', () => {
    expect(describeAutopay(none)).toBe('No autopay')
  })

  it('counts the days before the due date', () => {
    expect(describeAutopay({ ...none, autopay_rule: 'days_before_due', autopay_days: 5 })).toBe(
      'Autopays 5 days before due',
    )
  })

  it('says one day, not one days', () => {
    expect(describeAutopay({ ...none, autopay_rule: 'days_before_due', autopay_days: 1 })).toBe(
      'Autopays 1 day before due',
    )
  })

  it('reads a rule of zero days before due as the due date, which is the day it means', () => {
    expect(describeAutopay({ ...none, autopay_rule: 'days_before_due', autopay_days: 0 })).toBe(
      'Autopays on the due date',
    )
  })

  it('names the due date itself', () => {
    expect(describeAutopay({ ...none, autopay_rule: 'on_due_date' })).toBe(
      'Autopays on the due date',
    )
  })

  it('gives a day of the month its ordinal', () => {
    expect(describeAutopay({ ...none, autopay_rule: 'day_of_month', autopay_day: 21 })).toBe(
      'Autopays on the 21st',
    )
    expect(describeAutopay({ ...none, autopay_rule: 'day_of_month', autopay_day: 1 })).toBe(
      'Autopays on the 1st',
    )
    expect(describeAutopay({ ...none, autopay_rule: 'day_of_month', autopay_day: 22 })).toBe(
      'Autopays on the 22nd',
    )
  })

  it('ignores the figure the other kind of rule uses', () => {
    // A row can carry a figure in the unused column; this rule ignores it.
    expect(
      describeAutopay({ autopay_rule: 'on_due_date', autopay_days: 5, autopay_day: 21 }),
    ).toBe('Autopays on the due date')
  })
})

describe('forgetting a deleted connection', () => {
  /** Two logins, one subaccount each, with bills cached under each. */
  function seeded(): QueryClient {
    const client = testQueryClient()
    client.setQueryData(
      ['bills', 'connections'],
      [{ id: 'c1', label: 'Main account' }, { id: 'c2', label: 'kerbside' }],
    )
    client.setQueryData(
      ['bills', 'subaccounts', 'all'],
      [wireSubaccount(), wireSubaccount({ id: 'sub2', connection_id: 'c2' })],
    )
    client.setQueryData(['bills', 'subaccounts', 'c1'], [wireSubaccount()])
    client.setQueryData(['bills', 'bills', 'sub1'], [wireBill()])
    client.setQueryData(['bills', 'bills', 'sub2'], [wireBill({ id: 'b2', subaccount_id: 'sub2' })])
    return client
  }

  it('drops the bills of every subaccount that went with it', () => {
    const client = seeded()
    forgetBillConnection(client, 'c1')

    expect(client.getQueryData(['bills', 'bills', 'sub1'])).toBeUndefined()
    expect(client.getQueryData(['bills', 'subaccounts', 'c1'])).toBeUndefined()
  })

  it('leaves the other login alone', () => {
    const client = seeded()
    forgetBillConnection(client, 'c1')

    expect(client.getQueryData(['bills', 'bills', 'sub2'])).toHaveLength(1)
  })

  it('takes the connection out of the listings, so nothing renders it again', () => {
    const client = seeded()
    forgetBillConnection(client, 'c1')

    expect(client.getQueryData(['bills', 'connections'])).toEqual([
      { id: 'c2', label: 'kerbside' },
    ])
    expect(client.getQueryData(['bills', 'subaccounts', 'all'])).toEqual([
      wireSubaccount({ id: 'sub2', connection_id: 'c2' }),
    ])
  })
})

/** One connection as the settings screen holds it, signed in and pulling. */
function connection(over: Record<string, unknown> = {}) {
  return {
    biller: 'erie' as const,
    connected: true,
    needs_sign_in: false,
    credential_source: 'session' as const,
    sign_in_paused: '' as const,
    last_pull_status: 'ok' as const,
    last_pull_error: '',
    last_pulled_at: '2026-09-17T09:00:00Z',
    pull_enabled: true,
    pulling: false,
    ...over,
  }
}

/** One pull's answer, with nothing found unless a test says otherwise. */
function pullResult(over: Record<string, unknown> = {}): BillPullResult {
  return {
    status: 'ok',
    new: 0,
    amended: 0,
    unchanged: 0,
    documents: 0,
    challenge: null,
    error: '',
    notes: [],
    has_failure_screenshot: false,
    ...over,
  }
}

describe('how the last pull went', () => {
  const now = new Date('2026-09-17T11:00:00Z')

  it('puts what has to be done before what happened', () => {
    // A connection whose session went stale reports the sign-in, even though
    // it also has a perfectly good "pulled two hours ago" to report.
    expect(
      describePullStatus(
        connection({ needs_sign_in: true, last_pull_error: 'the session expired' }),
        now,
      ),
    ).toBe('Needs a sign-in. The session expired.')
  })

  it('does not ask for a sign-in the kept password is about to make', () => {
    const lapsed = connection({
      needs_sign_in: true,
      last_pull_status: 'needs_sign_in',
      credential_source: 'stored',
      last_pull_error: 'Erie Insurance refused the kept session',
    })
    expect(describePullStatus(lapsed, now)).toBe(
      'Session expired; the kept password signs in at the next update',
    )
    expect(
      describePullStatus(
        {
          ...lapsed,
          sign_in_paused: 'password_refused' as const,
          last_pull_error: 'Erie Insurance did not accept the kept password',
        },
        now,
      ),
    ).toBe(
      'Needs a sign-in. Erie Insurance did not accept the kept password. It is still kept but is not tried again on its own: sign in again, or press Update now to try it once more.',
    )
  })

  it('says a code nobody answered has paused the updates', () => {
    expect(
      describePullStatus(
        connection({ last_pull_status: 'challenge', sign_in_paused: 'code_needed' }),
        now,
      ),
    ).toBe('Erie Insurance asked for a code. Automatic updates wait until you sign in.')
  })

  it('names a parked code request rather than calling the pull a failure', () => {
    expect(describePullStatus(connection({ last_pull_status: 'challenge' }), now)).toBe(
      'A code is needed to finish the last update',
    )
  })

  it('carries the provider’s own words on a failure', () => {
    expect(
      describePullStatus(
        connection({ last_pull_status: 'failed', last_pull_error: 'the billing page moved' }),
        now,
      ),
    ).toBe('Last update failed. The billing page moved.')
  })

  it('says a sign-in that never landed in its own words', () => {
    expect(
      describePullStatus(
        connection({
          last_pull_status: 'sign_in_failed',
          last_pull_error:
            'The sign-in was closed before it finished, while Example Power was asking for a code.',
        }),
        now,
      ),
    ).toBe(
      'The last sign-in did not finish. The sign-in was closed before it finished, while Example Power was asking for a code.',
    )
  })

  it('tells a connection nobody has signed in to what it is for', () => {
    expect(describePullStatus(connection({ connected: false }), now)).toBe('Not connected')
  })

  it('says when the last pull was, and when the daily one is off', () => {
    expect(describePullStatus(connection(), now)).toBe('Updated 2 hours ago')
    expect(describePullStatus(connection({ pull_enabled: false }), now)).toBe(
      'Updated 2 hours ago; daily updates are off',
    )
  })

  it('says a pull is running before how the last one went', () => {
    // A sign-in clears the last pull and starts one; until it ends, the line
    // is about that one rather than "not updated yet".
    expect(
      describePullStatus(connection({ pulling: true, last_pull_status: 'failed' }), now),
    ).toBe('Fetching bills now…')
  })

  it('does not report a time for a connection that has never been pulled', () => {
    expect(describePullStatus(connection({ last_pulled_at: null }), now)).toBe(
      'Connected; not updated yet',
    )
  })
})

describe('what a pull did', () => {
  it('counts only the things there were any of', () => {
    expect(describePullResult(pullResult({ new: 3, amended: 1, unchanged: 2, documents: 2 }))).toBe(
      '3 new bills, 1 amended, 2 statements',
    )
    expect(describePullResult(pullResult({ new: 1 }))).toBe('1 new bill')
  })

  it('says in words that a run found nothing, rather than showing zeroes', () => {
    expect(describePullResult(pullResult({ unchanged: 4 }))).toBe(
      'Nothing new; 4 bills already on file',
    )
    expect(describePullResult(pullResult())).toBe('Nothing new')
  })

  it('reports a parked code request as unfinished, not as a failure', () => {
    expect(describePullResult(pullResult({ status: 'challenge' }))).toBe(
      'A code is needed to finish this update',
    )
  })

  it('carries the reason a pull failed or was refused', () => {
    expect(describePullResult(pullResult({ status: 'failed', error: 'the portal is down' }))).toBe(
      'The update failed. The portal is down.',
    )
    expect(
      describePullResult(pullResult({ status: 'needs_sign_in', error: 'the session expired' })),
    ).toBe('Sign in again. The session expired.')
    expect(describePullResult(pullResult({ status: 'failed' }))).toBe('The update failed')
  })
})

describe('what a challenge is asking for', () => {
  it('uses the provider’s own wording wherever it said any', () => {
    // Only the provider can name the phone the text went to.
    expect(challengeInstructions('sms', 'Enter the code sent to the number ending 00')).toBe(
      'Enter the code sent to the number ending 00',
    )
  })

  it('has a sentence of its own for a module that reported only a method', () => {
    expect(challengeInstructions('totp', '')).toContain('authenticator')
    expect(challengeInstructions('sms', '   ')).toContain('by text')
    expect(challengeInstructions('email', '')).toContain('emailed')
    expect(challengeInstructions('push', '')).toContain('Approve the sign-in on your phone')
    expect(challengeInstructions('captcha', '')).toContain('characters in the picture')
  })
})

describe('matching a provider history', () => {
  function account(over: Partial<BillAccountHistory> = {}): BillAccountHistory {
    return {
      subaccount_id: 'sub1',
      label: 'Billing account',
      series_id: 'ser1',
      matched: 0,
      settled: 0,
      with_statement: 0,
      unsettled: 0,
      cadence_gap_days: 0,
      ...over,
    }
  }

  it('posts to the whole login or to one billed account', async () => {
    const whole = respond({ statements_offered: true, accounts: [] })
    vi.stubGlobal('fetch', whole)
    await matchBillHistory({ connectionId: 'c1' })
    expect(calledWith(whole).url).toBe('/api/bill-payments/connections/c1/match')
    expect(calledWith(whole).method).toBe('POST')

    const one = respond({ statements_offered: true, accounts: [] })
    vi.stubGlobal('fetch', one)
    await matchBillHistory({ subaccountId: 'sub1' })
    expect(calledWith(one).url).toBe('/api/bill-payments/subaccounts/sub1/match')
  })

  it('counts statements filed and bills no payment matched', () => {
    const said = describeBillHistory(
      {
        statements_offered: true,
        accounts: [
          account({ matched: 3, settled: 12, with_statement: 10, unsettled: 1 }),
          account({ subaccount_id: 'sub2', matched: 1, settled: 4, with_statement: 4, unsettled: 1 }),
        ],
      },
      'Riverside Power',
    )
    expect(said.title).toBe('Linked 4 past payments to bills')
    expect(said.description).toBe(
      '14 statements on past payments, from 16 bills with a matching payment. 2 bills had no matching payment.',
    )
  })

  it('says why no statement came from a provider that offers none, and flags a mismatched schedule', () => {
    const said = describeBillHistory(
      {
        statements_offered: false,
        accounts: [account({ settled: 2, cadence_gap_days: 364 })],
      },
      'Example Mutual',
    )
    expect(said.title).toBe('No new payments matched')
    expect(said.description).toContain(
      '2 bills with a matching payment. Example Mutual offers no statement documents',
    )
    expect(said.description).toContain('Billing account’s bills come about once a year')
  })

  it('asks for a linked reminder when no billed account has one', () => {
    const said = describeBillHistory(
      { statements_offered: true, accounts: [account({ series_id: null })] },
      'Riverside Power',
    )
    expect(said.title).toBe('Nothing to match')
  })

  it('reads the bill a payment settled, coercing its amount', async () => {
    const fetcher = respond([
      {
        bill_id: 'b1',
        connection_id: 'c1',
        provider: 'Riverside Power (Main account)',
        due_on: '2026-09-03',
        amount_due: '80.00',
        status: 'paid',
        document_id: null,
      },
    ])
    vi.stubGlobal('fetch', fetcher)
    const settled = await listBillsSettledBy('txn-1')
    expect(settled[0].amount_due).toBe(parseMoney('80.00'))
    expect(calledWith(fetcher).url).toBe('/api/bill-payments/transactions/txn-1')
  })
})
