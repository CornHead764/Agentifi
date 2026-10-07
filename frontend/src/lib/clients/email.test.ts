import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  createEmailConnection,
  createMailRule,
  deleteEmailConnection,
  deleteMailRule,
  describePollStatus,
  forgetMailboxSecret,
  getMailboxSignIn,
  listRecentMail,
  listMailRules,
  pollMailbox,
  rereadMailboxMessage,
  signInMailboxWithPassword,
  startMailboxDeviceSignIn,
  suggestMailRule,
  tryMailRule,
  updateEmailConnection,
  updateMailRule,
  type EmailConnection,
  type MailRuleDraft,
} from './email'

/** One mailbox as the resource sends it. Every address is a documentation one. */
function wireConnection(over: Record<string, unknown> = {}) {
  return {
    id: 'mbx1',
    label: 'Bills',
    kind: 'graph',
    address: 'bills@example.com',
    client_id: '00000000-0000-0000-0000-000000000001',
    tenant: '00000000-0000-0000-0000-000000000002',
    host: '',
    port: null,
    username: '',
    folder: 'Inbox',
    enabled: true,
    connected: true,
    last_polled_at: '2026-09-18T09:00:00Z',
    last_poll_error: '',
    created_at: '2026-09-01T00:00:00Z',
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

describe('the mailbox resource', () => {
  it('lists, writes and removes a connection under /email', async () => {
    const create = respond(wireConnection())
    vi.stubGlobal('fetch', create)
    await createEmailConnection({ label: 'Bills', kind: 'graph', address: 'bills@example.com' })
    expect(calledWith(create)).toMatchObject({ url: '/api/email/connections', method: 'POST' })

    const patch = respond(wireConnection({ folder: 'Billing' }))
    vi.stubGlobal('fetch', patch)
    await updateEmailConnection('mbx1', { folder: 'Billing' })
    expect(calledWith(patch)).toMatchObject({
      url: '/api/email/connections/mbx1',
      method: 'PATCH',
    })

    const remove = respond(null, 204)
    vi.stubGlobal('fetch', remove)
    await deleteEmailConnection('mbx1')
    expect(calledWith(remove)).toMatchObject({
      url: '/api/email/connections/mbx1',
      method: 'DELETE',
    })
  })

  it('reads the mailbox with a POST and nothing in the body', async () => {
    const fetcher = respond({
      read: 0,
      bills: 0,
      otps: 0,
      unrecognised: 0,
      proposed: 0,
      failed: 0,
      error: '',
    })
    vi.stubGlobal('fetch', fetcher)

    await pollMailbox('mbx1')

    expect(calledWith(fetcher)).toMatchObject({
      url: '/api/email/connections/mbx1/poll',
      method: 'POST',
    })
  })

  it('drops the sealed secret by its own route, leaving the row', async () => {
    const fetcher = respond(wireConnection({ connected: false }))
    vi.stubGlobal('fetch', fetcher)

    const after = await forgetMailboxSecret('mbx1')

    expect(calledWith(fetcher)).toMatchObject({
      url: '/api/email/connections/mbx1/secret',
      method: 'DELETE',
    })
    expect(after.connected).toBe(false)
  })
})

describe('signing in to a mailbox', () => {
  it('sends nothing at all on the device-code path', async () => {
    // Office 365 refuses a password, so there is none to send: the body is
    // empty and the token lands on the server without passing through here.
    const fetcher = respond({
      session_id: 'sess1',
      user_code: 'HTKJ9FQR',
      verification_uri: 'https://microsoft.com/devicelogin',
      expires_at: '2026-09-18T09:15:00Z',
    })
    vi.stubGlobal('fetch', fetcher)

    await startMailboxDeviceSignIn('mbx1')

    const call = calledWith(fetcher)
    expect(call.url).toBe('/api/email/connections/mbx1/sign-in')
    expect(call.method).toBe('POST')
    expect(JSON.parse(call.body as string)).toEqual({})
  })

  it('carries the app password only on the IMAP path, and only that', async () => {
    const fetcher = respond(wireConnection({ kind: 'imap', connected: true }))
    vi.stubGlobal('fetch', fetcher)

    await signInMailboxWithPassword('mbx1', 'not-a-real-password')

    const call = calledWith(fetcher)
    expect(call.url).toBe('/api/email/connections/mbx1/sign-in')
    expect(JSON.parse(call.body as string)).toEqual({ password: 'not-a-real-password' })
  })

  it('answers a connection with no field for the secret it just sealed', async () => {
    // The shape is the guard: a response that could carry a password back is a
    // response that would end up in a query cache.
    vi.stubGlobal('fetch', respond(wireConnection({ kind: 'imap' })))

    const after = await signInMailboxWithPassword('mbx1', 'not-a-real-password')

    expect(after).not.toHaveProperty('password')
    expect(after.connected).toBe(true)
  })

  it('follows one session by id, so two sign-ins are never read as one', async () => {
    const fetcher = respond({ state: 'pending', error: '' })
    vi.stubGlobal('fetch', fetcher)

    await getMailboxSignIn('mbx1', 'sess1')

    expect(calledWith(fetcher).url).toBe('/api/email/connections/mbx1/sign-in/sess1')
  })
})

describe('how the last read went', () => {
  const now = new Date('2026-09-18T09:12:00Z')
  const connection = (over: Partial<EmailConnection> = {}) =>
    ({ ...wireConnection(), ...over }) as EmailConnection

  it('puts what has to be done before what happened', () => {
    // A mailbox nobody has signed in to says so, even with a folder and a
    // timestamp on the row from before the token was forgotten.
    expect(describePollStatus(connection({ connected: false }), now)).toBe('Not connected yet')
  })

  it('names the folder being watched, and when it was last read', () => {
    expect(describePollStatus(connection(), now)).toBe('Watching Inbox; read 12 minutes ago')
  })

  it('carries the mailbox’s own words on a failure', () => {
    expect(
      describePollStatus(connection({ last_poll_error: 'the consent was revoked' }), now),
    ).toBe('Could not read the mailbox: the consent was revoked')
  })

  it('says nothing is read while the switch is off', () => {
    expect(describePollStatus(connection({ enabled: false }), now)).toBe(
      'Reading is off, so nothing is taken from Inbox',
    )
  })

  it('does not report a time for a mailbox that has never been read', () => {
    expect(describePollStatus(connection({ last_polled_at: null }), now)).toBe(
      'Watching Inbox; nothing read yet',
    )
  })

  it('names the default folder of the kind for a row that stored none', () => {
    expect(describePollStatus(connection({ kind: 'imap', folder: '' }), now)).toContain(
      'Watching INBOX',
    )
    expect(describePollStatus(connection({ folder: '' }), now)).toContain('Watching Inbox')
  })
})


/** Invented: a lunch program that mails a receipt and deducts from the next paycheck. */
function ruleDraft(over: Partial<MailRuleDraft> = {}): MailRuleDraft {
  return {
    name: 'Lunch receipts',
    enabled: true,
    sender: '@lunch.example',
    subject_contains: 'Lunch Receipt',
    body_contains: '',
    amount_label: 'Receipt Total:',
    amount_pattern: '',
    date_label: 'Receipt Date:',
    date_pattern: '',
    reference_label: 'ReceiptID:',
    reference_pattern: '',
    issued_label: '',
    issued_pattern: '',
    minimum_label: '',
    minimum_pattern: '',
    payee: '',
    payee_label: 'Your receipt from',
    notes_label: '',
    notes_end_label: '',
    action: 'transaction',
    account_id: 'acct-checking',
    category_id: 'cat-dining',
    direction: 'expense',
    pad_income: false,
    income_account_id: null,
    income_category_id: null,
    income_payee: '',
    bill_connection_id: null,
    bill_subaccount_id: null,
    ...over,
  }
}

function wireRule(over: Record<string, unknown> = {}) {
  return { id: 'rule-1', sort_order: 0, created_at: '2026-09-01T00:00:00Z', ...ruleDraft(), ...over }
}

describe('mail rules', () => {
  it('lists, writes and removes a rule under /email/rules', async () => {
    const list = respond([wireRule()])
    vi.stubGlobal('fetch', list)
    await listMailRules()
    expect(calledWith(list)).toMatchObject({ url: '/api/email/rules', method: 'GET' })

    const create = respond(wireRule())
    vi.stubGlobal('fetch', create)
    await createMailRule(ruleDraft())
    expect(calledWith(create)).toMatchObject({ url: '/api/email/rules', method: 'POST' })

    const patch = respond(wireRule({ enabled: false }))
    vi.stubGlobal('fetch', patch)
    await updateMailRule('rule-1', { enabled: false })
    expect(calledWith(patch)).toMatchObject({ url: '/api/email/rules/rule-1', method: 'PATCH' })
    expect(JSON.parse(calledWith(patch).body as string)).toEqual({ enabled: false })

    const remove = respond(null, 204)
    vi.stubGlobal('fetch', remove)
    await deleteMailRule('rule-1')
    expect(calledWith(remove)).toMatchObject({ url: '/api/email/rules/rule-1', method: 'DELETE' })
  })

  it('sends the pasted message with the draft, and stores neither', async () => {
    const fetcher = respond({
      matched: true,
      amount: '4.50',
      date: '2026-09-27',
      reference: 'AB1234567',
      payee: 'The Corner Cafe',
      notes: 'AB1234567',
      error: '',
      would_post: [],
    })
    vi.stubGlobal('fetch', fetcher)

    await tryMailRule(ruleDraft(), {
      sender: 'receipts@lunch.example',
      subject: 'Lunch Receipt',
      text: 'Receipt Total: $4.50',
    })

    const call = calledWith(fetcher)
    expect(call).toMatchObject({ url: '/api/email/rules/try', method: 'POST' })
    expect(JSON.parse(call.body as string)).toMatchObject({
      rule: { name: 'Lunch receipts' },
      sample: {
        sender: 'receipts@lunch.example',
        subject: 'Lunch Receipt',
        text: 'Receipt Total: $4.50',
      },
    })
  })

  it('coerces the amounts a trial answers with, and nothing else', async () => {
    vi.stubGlobal(
      'fetch',
      respond({
        matched: true,
        amount: '4.50',
        date: '2026-09-27',
        reference: 'AB1234567',
        payee: 'The Corner Cafe',
        notes: 'AB1234567',
        error: '',
        would_post: [
          {
            account_id: 'acct-checking',
            amount: '-4.50',
            payee: 'The Corner Cafe',
            category_id: 'cat-dining',
          },
        ],
      }),
    )

    const trial = await tryMailRule(ruleDraft(), { sender: '', subject: '', text: '' })

    // Integer cents, not the wire string.
    expect(trial.amount).toBe(450)
    expect(trial.would_post[0].amount).toBe(-450)
    expect(trial.date).toBe('2026-09-27')
    expect(trial.reference).toBe('AB1234567')
  })

  it('reads one logged message again by the log row\u2019s own id', async () => {
    const fetcher = respond({ id: 'msg-1', outcome: 'rule', rule_id: 'rule-1' })
    vi.stubGlobal('fetch', fetcher)

    await rereadMailboxMessage('mbx1', 'msg-1')

    expect(calledWith(fetcher)).toMatchObject({
      url: '/api/email/connections/mbx1/messages/msg-1/reread',
      method: 'POST',
    })
  })
})

describe('the mail the assistant reads', () => {
  it('lists the recent mail of every mailbox together', async () => {
    const fetcher = respond([])
    vi.stubGlobal('fetch', fetcher)

    await listRecentMail()

    expect(calledWith(fetcher)).toMatchObject({ url: '/api/email/messages?limit=50', method: 'GET' })
  })

  it('asks for a drafted rule with a POST that carries nothing', async () => {
    const fetcher = respond({ rule: {}, dropped: [], sample: { sender: '', subject: '', text: '' } })
    vi.stubGlobal('fetch', fetcher)

    await suggestMailRule('log-receipt')

    const call = calledWith(fetcher)
    expect(call).toMatchObject({
      url: '/api/email/messages/log-receipt/suggest-rule',
      method: 'POST',
    })
    expect(call.body ?? undefined).toBeUndefined()
  })
})
