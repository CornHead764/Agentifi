import type { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'

import { SETTINGS_SECTIONS } from '@/components/shell/destinations'
import type { AssistantStatus } from '@/lib/clients/assistant'
import type { EmailConnection, MailboxMessage, MailRule } from '@/lib/clients/email'
import type { Category } from '@/lib/transactions/types'
import { renderScreen, testQueryClient } from '@/test/renderScreen'

import { EmailSettings } from './EmailSettings'

/** The Email page off a seeded cache. Every address, subject and figure is invented. */
function render(seed?: (client: QueryClient) => void) {
  const client = testQueryClient()
  seed?.(client)
  return renderScreen(<EmailSettings />, { client })
}

/** An Office 365 mailbox nobody has signed in to, on a documentation domain. */
const mailboxes: EmailConnection[] = [
  {
    id: 'mbx-bills',
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
    connected: false,
    last_polled_at: null,
    last_poll_error: '',
    created_at: '2026-09-01T00:00:00Z',
  },
]

/** One invented mail rule: a lunch receipt deducted from the next paycheck. */
const mailRules: MailRule[] = [
  {
    id: 'rule-lunch',
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
    pad_income: true,
    income_account_id: null,
    income_category_id: 'cat-pay',
    income_payee: '',
    bill_connection_id: null,
    bill_subaccount_id: null,
    sort_order: 0,
    created_at: '2026-09-01T00:00:00Z',
  },
]

/** The two categories the rule above files under, and nothing else. */
const categories: Category[] = ['Dining', 'Paycheck'].map((name, index) => ({
  id: index === 0 ? 'cat-dining' : 'cat-pay',
  parent_id: null,
  name,
  description: null,
  kind: index === 0 ? 'expense' : 'income',
  known_category_id: null,
  txf_id: null,
  txf_ids: [],
  is_user_assignable: true,
  is_editable: true,
  protected_reason: null,
  excluded_from_reports: false,
  excluded_from_spending_plan: false,
  excluded_from_category_list: false,
  logo_url: null,
  sort_order: index,
}))


/** Two messages the reader has seen: a receipt nothing claimed, and a code. */
const recent: MailboxMessage[] = [
  {
    id: 'log-receipt',
    connection_id: 'mbx-bills',
    message_id: '<r1@mail.example.invalid>',
    received_at: '2026-09-20T08:00:00Z',
    sender: 'receipts@lunch.example',
    subject: 'Your Lunch Receipt',
    biller: '',
    outcome: 'unrecognised',
    note: '',
    bill_id: null,
    document_id: null,
    rule_id: null,
    transaction_id: null,
    bill_connection_id: null,
  },
  {
    id: 'log-code',
    connection_id: 'mbx-bills',
    message_id: '<c1@mail.example.invalid>',
    received_at: '2026-09-20T07:00:00Z',
    sender: 'relay@example.invalid',
    subject: 'Text message',
    biller: '',
    outcome: 'otp',
    note: '',
    bill_id: null,
    document_id: null,
    rule_id: null,
    transaction_id: null,
    bill_connection_id: null,
  },
]

function assistant(overrides: Partial<AssistantStatus> = {}): AssistantStatus {
  return {
    configured: true,
    is_enabled: true,
    base_url: 'http://model.example.invalid/v1',
    model: 'local-model',
    name: '',
    has_key: false,
    allow_writes: false,
    apply_without_asking: false,
    tool_call_style: 'native',
    tools: [],
    ...overrides,
  }
}

function seeded(client: QueryClient, status: AssistantStatus = assistant()) {
  client.setQueryData(['email', 'connections'], mailboxes)
  client.setQueryData(['email', 'messages', 'all', ['mbx-bills']], recent)
  client.setQueryData(['email', 'rules'], mailRules)
  client.setQueryData(['categories'], categories)
  client.setQueryData(['assistant'], status)
}

describe('the email settings page', () => {
  it('is a section of its own in settings, after categories', () => {
    const paths = SETTINGS_SECTIONS.map((one) => one.path as string)
    expect(paths).toContain('/settings/email')
    expect(paths.indexOf('/settings/email')).toBe(paths.indexOf('/settings/categories-tags') + 1)
  })

  it('renders with no server behind it, which is where a household starts', () => {
    const markup = render()
    expect(markup).toContain('Mailboxes')
    expect(markup).toContain('Recent mail')
    expect(markup).toContain('Mail rules')
  })

  it('names a mailbox by its kind and address, and says it is not connected', () => {
    const markup = render(seeded)

    expect(markup).toContain('Office 365 · Bills')
    expect(markup).toContain('bills@example.com')
    expect(markup).toContain('Not connected yet')
    expect(markup).toContain('Add mailbox')
  })

  it('says what a connected mailbox is watching, and when it last read it', () => {
    const markup = render((client) => {
      seeded(client)
      client.setQueryData(['email', 'connections'], [
        { ...mailboxes[0], connected: true, last_polled_at: new Date().toISOString() },
      ])
    })

    expect(markup).toContain('Watching Inbox')
    expect(markup).toContain('Read now')
  })

  it('keeps every secret inside the sign-in dialog, never on the page', () => {
    const markup = render((client) => {
      seeded(client)
      client.setQueryData(['email', 'connections'], [
        { ...mailboxes[0], kind: 'imap', host: 'imap.example.com', username: 'bills@example.com' },
      ])
    })

    expect(markup).toContain('IMAP · Bills')
    expect(markup).not.toContain('type="password"')
  })

  it('lists recent mail with who sent it and what was made of it', () => {
    const markup = render(seeded)

    expect(markup).toContain('Your Lunch Receipt')
    expect(markup).toContain('receipts@lunch.example')
    expect(markup).toContain('Not a bill')
    expect(markup).toContain('Answered a code')
    // Each row has its own menu, named after the message.
    expect(markup).toContain('aria-label="Actions for Your Lunch Receipt"')
  })

  it('never shows a sign-in code, and has nothing to offer about one', () => {
    const markup = render(seeded)
    // The code row has no menu at all: nothing can be done with it from here.
    expect(markup).not.toContain('aria-label="Actions for Text message"')
  })

  it('offers the assistant setup in place when it cannot be asked yet', () => {
    const off = render((client) => seeded(client, assistant({ configured: false })))
    expect(off).toContain('>Set up the assistant</button>')
    expect(off).not.toContain('href="/settings/assistant"')
    expect(off).toContain('draft rules')

    const paused = render((client) => seeded(client, assistant({ is_enabled: false })))
    expect(paused).toContain('>Turn the assistant on</button>')

    const on = render(seeded)
    expect(on).not.toContain('Set up the assistant')
    expect(on).not.toContain('Turn the assistant on')
  })

  it('offers a rule of the household’s own for the mail no parser knows', () => {
    const markup = render(seeded)

    expect(markup).toContain('Add rule')
    expect(markup).toContain('Lunch receipts')
    expect(markup).toContain('Mail from anybody at lunch.example, with “Lunch Receipt” in the subject')
    expect(markup).toContain('Posts an expense as Dining; pads income as Paycheck')
    expect(markup).not.toContain('amount_label')
    // The rules card is what the bill providers' catalogue links to.
    expect(markup).toContain('id="mail-rules"')
  })

  it('says what a rule is for while there are none', () => {
    const markup = render((client) => {
      seeded(client)
      client.setQueryData(['email', 'rules'], [])
    })

    expect(markup).toContain('No rules yet')
    expect(markup).toContain('files a mailed bill on a')
  })

  it('says a bill rule files on its provider, and names a filed bill by it', () => {
    // An invented water utility with no connector, tracked from its emails.
    const markup = render((client) => {
      seeded(client)
      client.setQueryData(['bills', 'connections'], [
        { id: 'conn-water', biller: 'email-only', label: 'Example Water' },
      ])
      client.setQueryData(['bills', 'subaccounts', 'all'], [])
      client.setQueryData(['email', 'rules'], [
        {
          ...mailRules[0],
          id: 'rule-water',
          name: 'Water bill',
          action: 'bill',
          account_id: null,
          category_id: null,
          pad_income: false,
          bill_connection_id: 'conn-water',
        },
      ])
      client.setQueryData(['email', 'messages', 'all', ['mbx-bills']], [
        {
          ...recent[0],
          id: 'log-water',
          subject: 'Your water bill is ready',
          sender: 'billing@water.example.invalid',
          biller: 'email-only',
          outcome: 'bill',
          note: 'Rule "Water bill": filed a bill of 60.00 due 2026-10-20 on Example Water',
          bill_id: 'bill-1',
          document_id: 'doc-1',
          rule_id: 'rule-water',
          bill_connection_id: 'conn-water',
        },
      ])
    })

    expect(markup).toContain('Files a bill on Example Water')
    expect(markup).toContain('Filed a bill · Example Water')
    expect(markup).toContain('Statement')
    expect(markup).not.toContain('Emailed bills')
  })
})
