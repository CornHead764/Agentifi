/**
 * What the Settings sections read beyond the accounts, categories, tags and
 * spaces: bill providers, mailboxes, merchants, alerts, sign-in and the admin
 * screens. Invented names, round figures, addresses only at example.com.
 */

import { USER_ID } from './auth'
import { ACCOUNT, CATEGORY, CONNECTION, INSTITUTION, id } from './core'
import { SPACE_ID } from './shell'

const BILL_CONNECTION = { power: id('bcon', 1), water: id('bcon', 2), phone: id('bcon', 3) }
const MAILBOX = id('mbox', 1)
const AMAZON_ACCOUNT = id('mrch', 1)

function billConnection(seed: {
  id: string
  label: string
  username: string
  connected: boolean
  status: '' | 'ok' | 'needs_sign_in' | 'challenge' | 'failed'
  error?: string
}) {
  return {
    id: seed.id,
    biller: 'email-only',
    label: seed.label,
    username: seed.username,
    site: '',
    credential_source: 'stored',
    has_totp: false,
    second_factor: '',
    connected: seed.connected,
    signed_in_at: seed.connected ? '2026-06-10T14:00:00Z' : null,
    needs_sign_in: !seed.connected,
    sign_in_paused: '',
    autopay_rule: seed.connected ? 'days_before_due' : 'none',
    autopay_days: 3,
    autopay_day: 1,
    autopay_account_id: seed.connected ? ACCOUNT.checking : null,
    pull_enabled: true,
    pull_at: '05:00',
    last_pulled_at: seed.status ? '2026-06-15T10:00:00Z' : null,
    last_pull_status: seed.status,
    last_pull_error: seed.error ?? '',
    has_failure_screenshot: false,
    pulling: false,
    created_at: '2025-03-01T00:00:00Z',
  }
}

function billSubaccount(n: number, connection: string, label: string, masked: string) {
  return {
    id: id('bsub', n),
    connection_id: connection,
    biller: 'email-only',
    external_id: `sample-${n}`,
    label,
    masked_number: masked,
    is_selected: true,
    series_id: null,
    account_id: null,
  }
}

function alert(
  type: string,
  label: string,
  trigger: string,
  group: 'spending' | 'accounts' | 'bills' | 'planning' | 'summaries',
  threshold: 'none' | 'amount' | 'count' | 'percent',
  enabled: boolean,
) {
  return {
    type,
    label,
    trigger,
    group,
    threshold,
    channels: ['email', 'push', 'in_app'],
    evaluated: true,
    is_enabled: enabled,
    is_paused: false,
    channel_email: enabled,
    channel_push: false,
    channel_in_app: enabled,
    threshold_amount: threshold === 'amount' ? '500.00' : null,
    threshold_count: threshold === 'count' ? 3 : null,
    threshold_percent: threshold === 'percent' ? '90' : null,
    configured: true,
  }
}

function mailRule(n: number, name: string, action: 'transaction' | 'bill') {
  return {
    id: id('mrul', n),
    name,
    enabled: true,
    sender: action === 'bill' ? 'billing@example.com' : '@example.com',
    subject_contains: action === 'bill' ? 'Your statement is ready' : 'Receipt',
    body_contains: '',
    amount_label: action === 'bill' ? 'Amount due:' : 'Receipt Total:',
    amount_pattern: '',
    date_label: action === 'bill' ? 'Due date:' : '',
    date_pattern: '',
    reference_label: '',
    reference_pattern: '',
    issued_label: '',
    issued_pattern: '',
    minimum_label: '',
    minimum_pattern: '',
    payee: action === 'bill' ? '' : 'Example Coffee Roasters',
    payee_label: '',
    action,
    account_id: action === 'transaction' ? ACCOUNT.card : null,
    category_id: action === 'transaction' ? CATEGORY.dining : null,
    direction: 'expense',
    bill_connection_id: action === 'bill' ? BILL_CONNECTION.water : null,
    bill_subaccount_id: null,
    pad_income: false,
    income_account_id: null,
    income_category_id: null,
    income_payee: '',
    sort_order: n,
    created_at: '2026-01-10T00:00:00Z',
  }
}

function duplicateRow(
  n: number,
  date: string,
  payee: string,
  statement: string,
  category: string | null,
  source: string,
) {
  return {
    id: id('txn', n),
    date,
    amount: '-25.00',
    currency: 'USD',
    payee,
    statement_name: statement,
    category_name: category,
    notes: null,
    source,
    is_pending: false,
    is_transfer_leg: false,
  }
}

function transferLeg(n: number, accountId: string, accountName: string, amount: string, pairId: string | null) {
  return {
    transaction_id: id('txn', 900 + n),
    account_id: accountId,
    account_name: accountName,
    date: '2026-06-10',
    amount,
    currency: 'USD',
    payee: amount.startsWith('-') ? 'Transfer to savings' : 'Transfer from checking',
    source: 'simplefin',
    pair_id: pairId,
  }
}

const TRANSFER_WINDOW = { from: '2026-03-15', to: '2026-06-15', date_field: 'date' }

function transfer(n: number, amount: string, from: [string, string], to: [string, string], byHand: boolean) {
  const pair = id('pair', n)
  return {
    pair_id: pair,
    moved_on: `2026-06-${String(12 - n).padStart(2, '0')}`,
    amount,
    currency: 'USD',
    from: transferLeg(2 * n, from[0], from[1], `-${amount}`, pair),
    to: transferLeg(2 * n + 1, to[0], to[1], amount, pair),
    paired_by_hand: byHand,
  }
}

function amazonOrder(n: number, ordered: string, total: string, title: string, matched: boolean) {
  return {
    id: id('ordr', n),
    merchant: 'amazon',
    merchant_account_id: AMAZON_ACCOUNT,
    account_label: 'Household',
    order_number: `000-0000000-000000${n}`,
    ordered_on: ordered,
    total,
    currency: 'USD',
    status: 'Delivered',
    details_url: '',
    source: 'agentifi_json',
    kind: 'online',
    location: '',
    items: [
      {
        sku: `SAMPLE0000${n}`,
        title,
        quantity: 1,
        unit_price: total,
        total_owed: total,
        shipped_on: ordered,
        condition: 'New',
        url: '',
        catalog: null,
      },
    ],
    refunds: [],
    matched_transaction_ids: matched ? [id('txn', 800 + n)] : [],
    matched_transactions: matched
      ? [
          {
            id: id('txn', 800 + n),
            account_id: ACCOUNT.card,
            date: ordered,
            amount: `-${total}`,
            payee: 'Amazon',
            statement_name: 'SAMPLE MARKETPLACE',
          },
        ]
      : [],
    card_total: total,
    gift_card_amount: '0.00',
    tax: '0.00',
    paid_by_gift_card: false,
    cancelled: false,
    ignored: false,
  }
}

function remoteAccount(n: number, name: string, kind: string, balance: string, likely: string | null, match: string) {
  return {
    external_id: `ACT-${n}`,
    name,
    kind,
    institution: 'Example Bank',
    masked_number: likely === null ? '' : `00${n}`,
    balance,
    currency: 'USD',
    linked_account_id: null,
    suggested: likely === null ? [] : [likely],
    likely: match === 'number' || match === 'name' ? likely : null,
    match,
  }
}

function linkTarget(accountId: string, name: string, kind: string, balance: string) {
  return { id: accountId, name, kind, masked_number: '', balance, sync_floor_on: '2026-06-01', linked_to: '' }
}

export const SETTINGS = {
  'GET /accounts/valuation-sources': { asset_types: ['real_estate', 'vehicle'], configured: ['real_estate'] },
  'GET /ignored-accounts': [
    {
      id: id('acct', 50),
      name: 'Old Holiday Club Savings Account From A Previous Bank',
      type: 'savings',
      kind: 'cash',
      masked_number: '0000',
      connection_id: null,
      institution_id: INSTITUTION,
      institution: 'Example Bank',
      balance: '0.00',
      ignored_at: '2026-02-01T00:00:00Z',
    },
    {
      id: id('acct', 51),
      name: 'Closed Club Account',
      type: 'savings',
      kind: 'cash',
      masked_number: '0001',
      connection_id: null,
      institution_id: INSTITUTION,
      institution: 'Example Bank',
      balance: '0.00',
      ignored_at: '2026-02-01T00:00:00Z',
    },
  ],
  [`GET /connections/${CONNECTION}/candidates`]: {
    remote: [
      remoteAccount(11, 'Everyday Checking', 'cash', '5000.00', ACCOUNT.checking, 'name'),
      remoteAccount(12, 'Sample Rewards Card With A Rather Long Name At The Bank', 'credit_card', '-800.00', ACCOUNT.card, 'tie'),
      remoteAccount(13, 'Sample Coin Wallet (0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9)', 'investment', '0.12', ACCOUNT.brokerage, 'weak'),
      remoteAccount(14, 'Joint Savings', 'cash', '300.00', null, 'none'),
    ],
    local: [
      linkTarget(ACCOUNT.checking, 'Everyday Checking', 'cash', '5000.00'),
      linkTarget(ACCOUNT.card, 'Sample Rewards Card', 'credit_card', '-800.00'),
      linkTarget(ACCOUNT.brokerage, 'Brokerage', 'investment', '20000.00'),
    ],
    ignored: [
      {
        id: id('ign', 1),
        external_id: 'ACT-20',
        name: 'Sample Dust Wallet',
        institution: 'Example Exchange',
        masked_number: '',
        ignored_at: '2026-02-01T00:00:00Z',
      },
    ],
  },

  'GET /bills/connections': [
    billConnection({ id: BILL_CONNECTION.power, label: 'City Power & Light', username: 'sample-user@example.com', connected: true, status: 'ok' }),
    billConnection({ id: BILL_CONNECTION.water, label: 'Riverside Municipal Water And Sewer Utility District', username: 'sample-user@example.com', connected: true, status: 'failed', error: 'The statement page did not load.' }),
    billConnection({ id: BILL_CONNECTION.phone, label: 'Example Mobile', username: 'sample-user', connected: false, status: 'needs_sign_in' }),
  ],
  'GET /bills/subaccounts': [
    billSubaccount(1, BILL_CONNECTION.power, 'Home electric', '0000'),
    billSubaccount(2, BILL_CONNECTION.water, 'Water and sewer', '0001'),
    billSubaccount(3, BILL_CONNECTION.phone, 'Family plan', '0002'),
  ],
  'GET /bills/subaccounts/:id/bills': [],
  'GET /bills/challenges': [],
  'GET /bills/agent': {
    configured: true,
    healthy: true,
    error: '',
    providers: [
      {
        id: 'email-only',
        name: 'Any company that emails its bills',
        access: 'email',
        sign_in: { kinds: [], prompt: '' },
        challenges: [],
        session_persists: false,
        keepalive_days: 0,
        reports_autopay: false,
        has_documents: true,
      },
    ],
  },

  'GET /email/connections': [
    {
      id: MAILBOX,
      label: 'Household mail',
      kind: 'imap',
      address: 'sample-user@example.com',
      client_id: '',
      tenant: '',
      host: 'imap.example.com',
      port: 993,
      username: 'sample-user@example.com',
      folder: 'INBOX',
      enabled: true,
      connected: true,
      last_polled_at: '2026-06-15T11:00:00Z',
      last_poll_error: '',
      created_at: '2025-06-01T00:00:00Z',
    },
  ],
  'GET /email/messages': [
    {
      id: id('mmsg', 1),
      connection_id: MAILBOX,
      message_id: '<sample-1@example.com>',
      received_at: '2026-06-14T13:00:00Z',
      sender: 'billing@example.com',
      subject: 'Your statement is ready',
      biller: 'email-only',
      outcome: 'bill',
      note: '',
      bill_id: id('bill', 1),
      document_id: null,
      rule_id: null,
      transaction_id: null,
      bill_connection_id: BILL_CONNECTION.power,
    },
    {
      id: id('mmsg', 2),
      connection_id: MAILBOX,
      message_id: '<sample-2@example.com>',
      received_at: '2026-06-13T09:00:00Z',
      sender: 'newsletter@example.com',
      subject: 'A very long subject line from a sender that no rule recognises at all',
      biller: '',
      outcome: 'unrecognised',
      note: '',
      bill_id: null,
      document_id: null,
      rule_id: null,
      transaction_id: null,
      bill_connection_id: null,
    },
  ],
  'GET /email/rules': [
    mailRule(1, 'Coffee receipts', 'transaction'),
    mailRule(2, 'Riverside Municipal Water And Sewer Utility District statements', 'bill'),
  ],

  'GET /merchants/:merchant/agent': { configured: true, reachable: true, detail: '', unavailable: '' },
  'GET /merchants/amazon/accounts': [
    {
      id: AMAZON_ACCOUNT,
      merchant: 'amazon',
      label: 'Household',
      name: 'Amazon (Household)',
      orders: 2,
      email: 'sample-user@example.com',
      connected: true,
      signed_in_at: '2026-06-01T12:00:00Z',
      has_password: true,
      has_totp: false,
      second_factor: 'email',
      sign_in_paused: '',
      sync_enabled: true,
      sync_days: 30,
      last_synced_at: '2026-06-15T09:00:00Z',
      last_sync_status: 'ok',
      last_sync_error: '',
      has_failure_screenshot: false,
      needs_sign_in: false,
      pulling: false,
      gift_card_account_id: null,
      gift_card_balance: '25.00',
      gift_card_balance_at: '2026-06-15T09:00:00Z',
      backfill: null,
      created_at: '2025-06-01T00:00:00Z',
    },
  ],
  'GET /merchants/costco/accounts': [],
  'GET /merchants/:merchant/accounts/:id/backfill': { wanting: 0 },
  'GET /merchants/amazon/summary': {
    accounts: 1,
    orders: 2,
    items: 2,
    charges: 2,
    merchant_transactions: 3,
    matched_transactions: 1,
    newest_order: '2026-06-08',
    oldest_order: '2026-05-20',
  },
  'GET /merchants/costco/summary': {
    accounts: 0,
    orders: 0,
    items: 0,
    charges: 0,
    merchant_transactions: 0,
    matched_transactions: 0,
    newest_order: null,
    oldest_order: null,
  },
  'GET /merchants/amazon/orders': {
    orders: [
      amazonOrder(1, '2026-06-08', '40.00', 'Sample Stainless Steel Insulated Water Bottle With A Very Long Product Title, 32 oz', true),
      amazonOrder(2, '2026-05-20', '15.00', 'Example Paperback Notebook', false),
    ],
    total: 2,
  },
  'GET /merchants/costco/orders': { orders: [], total: 0 },

  'GET /notifications/settings': {
    alerts: [
      alert('large_transaction', 'Large transaction', 'A single transaction over the amount', 'spending', 'amount', true),
      alert('low_balance', 'Low balance', 'A cash account falls below the amount', 'accounts', 'amount', true),
      alert('bill_due', 'Bill due', 'A bill is due within the days', 'bills', 'count', true),
      alert('plan_overspent', 'Spending plan nearly spent', 'Spending reaches a share of the plan', 'planning', 'percent', false),
      alert('weekly_summary', 'Weekly summary', 'Every Monday morning', 'summaries', 'none', true),
    ],
    all_paused: false,
    email_enabled: true,
  },
  'GET /notifications/push': {
    public_key: 'sample-public-key',
    subscriptions: [
      { id: id('push', 1), user_agent: 'Firefox on Linux', created_at: '2026-01-05T00:00:00Z', last_used_at: '2026-06-14T08:00:00Z' },
    ],
  },

  'GET /auth/totp': { enabled: true, recovery_codes_remaining: 8 },
  'GET /auth/passkeys': [
    {
      id: id('pkey', 1),
      name: 'Laptop fingerprint reader on the home office desk',
      created_at: '2026-01-05T00:00:00Z',
      last_used_at: '2026-06-14T08:00:00Z',
      transports: ['internal'],
      rp_id: 'localhost',
    },
    {
      id: id('pkey', 2),
      name: 'Security key',
      created_at: '2026-02-10T00:00:00Z',
      last_used_at: null,
      transports: ['usb'],
      rp_id: 'localhost',
    },
  ],
  'GET /auth/oidc/config': { enabled: false, provider_name: '' },

  'GET /admin/users': [
    {
      id: USER_ID,
      email: 'sam@example.com',
      full_name: 'Sam Sample',
      is_active: true,
      is_superuser: true,
      is_verified: true,
      must_change_password: false,
      created_at: '2025-01-01T00:00:00Z',
      last_login_at: '2026-06-15T08:00:00Z',
      has_password: true,
      has_totp: true,
      has_oidc: false,
      oidc_issuer: null,
      passkey_count: 2,
      memberships: [
        { id: id('member', 1), space_id: SPACE_ID, space_name: 'Sample Household', role: 'owner', accepted: true },
      ],
    },
    {
      id: id('user', 2),
      email: 'a-very-long-sample-address-for-wrapping@example.com',
      full_name: null,
      is_active: true,
      is_superuser: false,
      is_verified: false,
      must_change_password: true,
      created_at: '2026-06-01T00:00:00Z',
      last_login_at: null,
      has_password: true,
      has_totp: false,
      has_oidc: false,
      oidc_issuer: null,
      passkey_count: 0,
      memberships: [
        { id: id('member', 2), space_id: SPACE_ID, space_name: 'Sample Household', role: 'viewer', accepted: false },
      ],
    },
  ],
  'GET /admin/spaces': [
    {
      id: SPACE_ID,
      name: 'Sample Household',
      primary_currency: 'USD',
      timezone: 'America/New_York',
      created_at: '2025-01-01T00:00:00Z',
      members: [
        { membership_id: id('member', 1), user_id: USER_ID, email: 'sam@example.com', full_name: 'Sam Sample', role: 'owner', accepted: true },
        { membership_id: id('member', 2), user_id: id('user', 2), email: 'a-very-long-sample-address-for-wrapping@example.com', full_name: null, role: 'viewer', accepted: false },
      ],
    },
    {
      id: id('space', 2),
      name: 'Side Business',
      primary_currency: 'USD',
      timezone: 'America/New_York',
      created_at: '2025-04-01T00:00:00Z',
      members: [],
    },
  ],
  'GET /admin/oidc': {
    enabled: false,
    provider_name: '',
    discovery_url: '',
    client_id: '',
    has_client_secret: false,
    scopes: ['openid', 'email', 'profile'],
    auto_register: false,
    require_verified_email: true,
    link_existing_email: false,
    sources: {},
    callback_url: 'https://agentifi.example.com/auth/oidc/callback',
    configured: false,
  },
  'GET /admin/server': {
    commit: '0f3c9a51b2d7e8c4a6f1093d5b7e2c8a4f6d1b30',
    built_at: '2026-03-04T08:15:00Z',
    modified: false,
    go_version: 'go1.26.8',
    time_zone: 'EDT (UTC-04:00)',
    schema_version: 1,
  },
  'GET /admin/server/settings': {
    settings: [
      {
        key: 'SIMPLEFIN_ENABLED',
        group: 'Banks and sync',
        label: 'SimpleFIN bank connections',
        help: 'Lets a space connect its banks with a SimpleFIN setup token, and the daily sync pull them.',
        kind: 'toggle',
        value: 'true',
        default: 'false',
        source: 'database',
        live: true,
        pending_restart: false,
      },
      {
        key: 'SYNC_ENABLED',
        group: 'Banks and sync',
        label: 'Daily sync',
        help: 'The daily bank sync and the bill and merchant pulls. A sync on request works either way.',
        kind: 'toggle',
        value: 'true',
        default: 'true',
        source: 'default',
        live: false,
        pending_restart: false,
      },
      {
        key: 'SYNC_AT',
        group: 'Banks and sync',
        label: 'Daily sync time',
        help: 'In the server’s time zone.',
        kind: 'time',
        value: '05:00',
        default: '04:00',
        source: 'database',
        live: false,
        pending_restart: true,
      },
      {
        key: 'SUPPORTED_CURRENCIES',
        group: 'Features',
        label: 'Currencies',
        help: 'The ISO codes a space may report in, comma-separated.',
        kind: 'list',
        value: 'USD,EUR,GBP,CAD,AUD,CHF,JPY,MXN,BRL,INR,SEK,DKK,NOK,PLN,CZK,NZD',
        default: 'USD,EUR,GBP,CAD,AUD,CHF,JPY,MXN,BRL,INR,SEK,DKK,NOK,PLN,CZK,NZD',
        source: 'default',
        live: false,
        pending_restart: false,
      },
      {
        key: 'FRONTEND_URL',
        group: 'Sign-in',
        label: 'App address',
        help: 'The address people reach the app on, scheme and port included.',
        kind: 'text',
        value: 'https://agentifi.example.com',
        default: 'http://localhost:5173',
        source: 'environment',
        live: false,
        pending_restart: false,
      },
      {
        key: 'LOGIN_MAX_ATTEMPTS',
        group: 'Sign-in',
        label: 'Password attempts per window',
        help: 'From one address.',
        kind: 'number',
        value: '20',
        default: '20',
        source: 'default',
        live: true,
        pending_restart: false,
      },
    ],
  },
  'GET /admin/backups': {
    enabled: true,
    directory: '/backups',
    at: '03:30',
    timezone: 'America/New_York',
    keep_days: 14,
    recipients: [
      {
        recipient: 'age1n946z2m9xyhkwem4vn9yj7hj7trelehnwv6q09vqqkzfq4sz3v8qp6rp0m',
        label: 'Kept in the fire safe',
        added_at: '2026-03-01T12:00:00Z',
        kind: 'age',
        fingerprint: '',
      },
      {
        recipient: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHtfBk58AasAD2LR2xeQ/qM6+2BhtSPUDwJP25s93+Bw',
        label: 'backup-test@example.invalid',
        added_at: '2026-03-02T12:00:00Z',
        kind: 'ssh-ed25519',
        fingerprint: 'SHA256:zeupREKRB/xQzRoKxYO2h+MbTWn/XoCwc0PDgie88Zk',
      },
    ],
    sources: { at: 'database', keep_days: 'environment', recipients: 'database' },
    encrypted: true,
    next_run: '2026-03-05T08:30:00Z',
    running: false,
    problem: null,
    dump_version: '17.6',
    server_version: '17.6',
    runs: [
      {
        trigger: 'nightly',
        status: 'failed',
        started_at: '2026-03-04T08:30:00Z',
        finished_at: '2026-03-04T08:31:00Z',
        set_name: null,
        encrypted: false,
        bytes: 0,
        error: 'backup: pg_dump: connection to server failed: the database system is starting up',
      },
    ],
    sets: [
      {
        name: '2026-03-03_033000_nightly',
        created_at: '2026-03-03T08:30:00Z',
        trigger: 'nightly',
        encrypted: true,
        recipients: ['age1n946z2m9xyhkwem4vn9yj7hj7trelehnwv6q09vqqkzfq4sz3v8qp6rp0m'],
        verified: true,
        intact: true,
        problem: null,
        bytes: 5_242_880,
        schema_version: 1,
        parts: [
          { part: 'database', bytes: 4_194_304, count: 0 },
          { part: 'attachments', bytes: 1_048_000, count: 12 },
          { part: 'secrets', bytes: 576, count: 5 },
        ],
        key_matches: true,
      },
      {
        name: '2026-02-20_120000_manual',
        created_at: '2026-02-20T17:00:00Z',
        trigger: 'manual',
        encrypted: false,
        recipients: [],
        verified: true,
        intact: true,
        problem: null,
        bytes: 3_145_728,
        schema_version: 1,
        parts: [{ part: 'database', bytes: 3_145_728, count: 0 }],
        key_matches: null,
      },
    ],
  },

  'GET /transfers': {
    transfers: [
      transfer(1, '500.00', [ACCOUNT.checking, 'Everyday Checking'], [ACCOUNT.savings, 'Rainy Day Savings'], false),
      transfer(2, '800.00', [ACCOUNT.checking, 'Everyday Checking'], [ACCOUNT.card, 'Sample Rewards Card'], true),
    ],
    window: TRANSFER_WINDOW,
    orphan_count: 1,
  },
  'GET /transfers/candidates': {
    candidates: [transferLeg(20, ACCOUNT.checking, 'Everyday Checking', '-100.00', null)],
    window: TRANSFER_WINDOW,
  },
  'GET /transfers/orphans': {
    orphans: [transferLeg(21, ACCOUNT.savings, 'Rainy Day Savings', '250.00', id('pair', 9))],
  },

  'GET /transaction-duplicates': {
    count: 1,
    pairs: [
      {
        id: id('dup', 1),
        account_id: ACCOUNT.checking,
        account_name: 'Everyday Checking',
        days_apart: 1,
        suggested_keep_id: id('txn', 401),
        first: duplicateRow(401, '2026-03-15', 'Corner Market', 'Corner Market', 'Groceries', 'simplifi_import'),
        second: duplicateRow(402, '2026-03-16', 'Corner Market', 'POS DEBIT CORNER MKT #0042 SPRINGFIELD', null, 'sync'),
      },
    ],
  },
}
