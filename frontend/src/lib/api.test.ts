import { afterEach, describe, expect, it, vi } from 'vitest'

import { activeSpaceId, adoptUser, resetActiveSpace, setActiveSpace } from './activeSpace'
import { api, ApiError, auditUndeclaredMoney, blob, coerceMoney, type MoneyShape, upload } from './api'
import { formatMoney, sumMoney, type Money } from './money'

interface Split {
  category_id: string
  amount: Money
}

interface Transaction {
  id: string
  statement_name: string
  payee: string
  amount: Money
  amount_primary: Money
  running_balance: Money | null
  splits: Split[]
}

interface AccountPage {
  account: { id: string; name: string; balance: Money }
  transactions: Transaction[]
}

const ACCOUNT_PAGE: MoneyShape<AccountPage> = {
  account: { balance: 'money' },
  transactions: {
    amount: 'money',
    amount_primary: 'money',
    running_balance: 'money',
    splits: { amount: 'money' },
  },
}

function body() {
  return {
    account: { id: 'a1', name: 'Checking', balance: '1900.00' },
    transactions: [
      {
        id: 't1',
        statement_name: 'SQ *CORNER COFFEE 0042',
        payee: 'Corner Coffee',
        amount: '-4.50',
        amount_primary: '-4.50',
        running_balance: '1895.50',
        splits: [],
      },
      {
        id: 't2',
        statement_name: 'COSTCO WHSE #0123',
        payee: 'Costco',
        amount: '-212.00',
        amount_primary: '-212.00',
        running_balance: null,
        splits: [
          { category_id: 'groceries', amount: '-180.00' },
          { category_id: 'household', amount: '-32.00' },
        ],
      },
    ],
  }
}

describe('coerceMoney', () => {
  it('coerces declared fields at every depth, including through arrays', () => {
    const page = coerceMoney<AccountPage>(body(), ACCOUNT_PAGE)

    expect(page.account.balance).toBe(190_000)
    expect(page.transactions[0].amount).toBe(-450)
    expect(page.transactions[1].splits[1].amount).toBe(-3200)
  })

  it('leaves the amounts addable and formattable, which is the whole point', () => {
    const page = coerceMoney<AccountPage>(body(), ACCOUNT_PAGE)
    const spent = sumMoney(page.transactions.map((txn) => txn.amount))

    expect(spent).toBe(-21_650)
    expect(formatMoney(spent, { signs: 'spend' })).toBe('$216.50')
  })

  it('leaves undeclared fields untouched', () => {
    const page = coerceMoney<AccountPage>(body(), ACCOUNT_PAGE)

    expect(page.transactions[0].statement_name).toBe('SQ *CORNER COFFEE 0042')
    expect(page.account.name).toBe('Checking')
  })

  it('passes null through instead of coercing it to zero', () => {
    const page = coerceMoney<AccountPage>(body(), ACCOUNT_PAGE)

    // "no running balance yet" and "$0.00" are different facts.
    expect(page.transactions[1].running_balance).toBeNull()
  })

  it('is a no-op for a resource with no money', () => {
    expect(coerceMoney<{ status: string }>({ status: 'ok' })).toEqual({ status: 'ok' })
  })
})

describe('auditUndeclaredMoney', () => {
  it('names the path of a money field left out of the shape', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const partial: MoneyShape<AccountPage> = { account: { balance: 'money' } }

    auditUndeclaredMoney(coerceMoney<AccountPage>(body(), partial))

    expect(warn).toHaveBeenCalled()
    expect(warn.mock.calls.map(([message]) => message).join('\n')).toContain(
      '$.transactions[0].amount',
    )
    warn.mockRestore()
  })

  it('stays quiet once every money field is declared', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})

    auditUndeclaredMoney(coerceMoney<AccountPage>(body(), ACCOUNT_PAGE))

    expect(warn).not.toHaveBeenCalled()
    warn.mockRestore()
  })
})

describe('a column of bare amounts', () => {
  // `column_totals` is `Money[]`, an array of amounts, not a single amount.
  interface Summary {
    columns: string[]
    column_totals: Money[]
    total: Money
  }

  const SUMMARY: MoneyShape<Summary> = { column_totals: 'money', total: 'money' }

  it('is coerced element by element', () => {
    const body = { columns: ['2026-07', '2026-08'], column_totals: ['-1200.00', '-830.50'], total: '-2030.50' }
    const coerced = coerceMoney<Summary>(body, SUMMARY)

    expect(formatMoney(coerced.column_totals[0])).toBe('-$1,200.00')
    expect(formatMoney(coerced.column_totals[1])).toBe('-$830.50')
    expect(formatMoney(sumMoney(coerced.column_totals))).toBe(formatMoney(coerced.total))
  })

  it('survives the empty column set a report over no data returns', () => {
    const coerced = coerceMoney<Summary>({ columns: [], column_totals: [], total: '0.00' }, SUMMARY)
    expect(coerced.column_totals).toEqual([])
    expect(formatMoney(coerced.total)).toBe('$0.00')
  })
})

/** Without the space header the API falls back to the caller's oldest membership. */
describe('the space header', () => {
  function respond(status: number, body: unknown, requestId?: string) {
    return vi.fn().mockResolvedValue({
      ok: status >= 200 && status < 300,
      status,
      headers: { get: (name: string) => (name === 'X-Request-Id' ? (requestId ?? null) : null) },
      text: () => Promise.resolve(JSON.stringify(body)),
    })
  }

  function headersOf(fetcher: ReturnType<typeof respond>): Record<string, string> {
    const init: unknown = fetcher.mock.calls[0]?.[1]
    return (init as { headers: Record<string, string> }).headers
  }

  afterEach(() => {
    resetActiveSpace()
    vi.unstubAllGlobals()
  })

  it('is absent while nobody has chosen, which is every single-space household', async () => {
    const fetcher = respond(200, [])
    vi.stubGlobal('fetch', fetcher)
    adoptUser('u-ada')

    await api.get('/accounts')

    expect(headersOf(fetcher)['X-Space-Id']).toBeUndefined()
  })

  it('names the chosen space on every request once one is chosen', async () => {
    const fetcher = respond(200, [])
    vi.stubGlobal('fetch', fetcher)
    adoptUser('u-ada')
    setActiveSpace('s-cabin')

    await api.get('/accounts')

    expect(headersOf(fetcher)['X-Space-Id']).toBe('s-cabin')
  })

  it('drops a space the server will not resolve, so the next request falls back', async () => {
    // A membership revoked since the space was chosen.
    vi.stubGlobal('fetch', respond(404, { detail: 'Space not found' }))
    adoptUser('u-ada')
    setActiveSpace('s-revoked')

    await expect(api.get('/accounts')).rejects.toBeInstanceOf(ApiError)

    expect(activeSpaceId()).toBeNull()
  })

  it('keeps the choice when it is the row that is missing, not the space', async () => {
    vi.stubGlobal('fetch', respond(404, { detail: 'Not found' }))
    adoptUser('u-ada')
    setActiveSpace('s-cabin')

    await expect(api.get('/transactions/nope')).rejects.toBeInstanceOf(ApiError)

    expect(activeSpaceId()).toBe('s-cabin')
  })

  it('refuses the app’s own page as a response, which is how an older server says “no such path”', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        headers: { get: (name: string) => (name === 'Content-Type' ? 'text/html; charset=utf-8' : null) },
        text: () => Promise.resolve('<!doctype html><div id="root"></div>'),
      }),
    )

    const error = await api.get('/unused').catch((thrown: unknown) => thrown)

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).status).toBe(404)
  })

  it('carries the server’s request id on a failure, which is what a user can quote', async () => {
    vi.stubGlobal('fetch', respond(500, { detail: 'boom' }, 'req-42'))

    const error = await api.get('/accounts').catch((thrown: unknown) => thrown)

    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).requestId).toBe('req-42')
  })
})

describe('upload and blob', () => {
  function page(contentType: string, text: string) {
    return vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: (name: string) => (name === 'Content-Type' ? contentType : null) },
      text: () => Promise.resolve(text),
      blob: () => Promise.resolve(new Blob([text], { type: contentType })),
    })
  }

  afterEach(() => vi.unstubAllGlobals())

  it('refuses the app’s own page as an upload answer, as every JSON call does', async () => {
    vi.stubGlobal('fetch', page('text/html', '<!doctype html>'))

    const error = await upload('/imports', new File(['x'], 'x.csv')).catch((thrown: unknown) => thrown)

    expect((error as ApiError).status).toBe(404)
  })

  it('coerces the money an upload answer declares', async () => {
    vi.stubGlobal('fetch', page('application/json', JSON.stringify({ total: '12.34' })))

    const result = await upload<{ total: Money }>('/imports', new File(['x'], 'x.csv'), {}, 'file', {
      total: 'money',
    })

    expect(result.total).toBe(1234)
  })

  it('hands back an HTML attachment as the file it is', async () => {
    vi.stubGlobal('fetch', page('text/html', '<p>receipt</p>'))

    const file = await blob('/attachments/a1/content')

    expect(file.type).toBe('text/html')
  })
})
