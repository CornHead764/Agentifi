/** What the shell reads on every page: the space, the bell, the connection and the assistant. */

import { USER_ID } from './auth'
import { CONNECTION, INSTITUTION, id } from './core'

export const SPACE_ID = id('space', 1)

export const SPACE = {
  id: SPACE_ID,
  name: 'Sample Household',
  primary_currency: 'USD',
  timezone: 'America/New_York',
  default_date_range: '',
  sidebar_account_types: null,
  role: 'owner',
  can_write: true,
  is_owner: true,
  joined_at: '2025-01-01T00:00:00Z',
}

export const SHELL = {
  'GET /spaces': [SPACE, { ...SPACE, id: id('space', 2), name: 'Side Business', role: 'member', is_owner: false }],
  'GET /spaces/current': SPACE,
  'GET /spaces/invitations': [],
  'GET /setup-guide': {
    dismissed: false,
    complete: false,
    skipped: ['bills'],
    steps: [
      { id: 'import', state: 'done', optional: false },
      { id: 'connect', state: 'done', optional: false },
      { id: 'match', state: 'open', optional: false },
      { id: 'assistant', state: 'open', optional: true },
      { id: 'bills', state: 'skipped', optional: true },
      { id: 'backups', state: 'open', optional: true },
    ],
  },
  'GET /spaces/:id/members': [
    {
      id: id('member', 1),
      user_id: USER_ID,
      email: 'sam@example.com',
      full_name: 'Sam Sample',
      role: 'owner',
      invited_at: '2025-01-01T00:00:00Z',
      accepted_at: '2025-01-01T00:00:00Z',
    },
    {
      id: id('member', 2),
      user_id: id('user', 2),
      email: 'alex@example.com',
      full_name: null,
      role: 'viewer',
      invited_at: '2026-06-01T00:00:00Z',
      accepted_at: null,
    },
  ],
  'GET /notifications': {
    notifications: [
      {
        id: id('note', 1),
        alert_type: 'large_transaction',
        label: 'Large transaction',
        title: 'A $1,000.00 charge at Example Furniture Co.',
        body: 'On Sample Rewards Card.',
        url: '/transactions',
        read_at: null,
        created_at: '2026-06-14T15:00:00Z',
      },
      {
        id: id('note', 2),
        alert_type: 'bill_due',
        label: 'Bill due',
        title: 'Electric bill due in 3 days',
        body: '$100.00 to City Power & Light.',
        url: '/upcoming',
        read_at: '2026-06-13T15:00:00Z',
        created_at: '2026-06-12T15:00:00Z',
      },
    ],
    unread: 1,
  },
  'GET /connections': {
    simplefin_enabled: true,
    connections: [
      {
        id: CONNECTION,
        name: 'SimpleFIN',
        status: 'active',
        status_detail: null,
        needs_setup_token: false,
        bank_warnings: [],
        ignored: [],
        last_sync_at: '2026-06-15T06:00:00Z',
        last_successful_sync_at: '2026-06-15T06:00:00Z',
        retry_not_before: null,
        created_at: '2025-01-01T00:00:00Z',
        sync: null,
      },
    ],
    schedule: { enabled: true, at: '04:00', time_zone: 'America/New_York', next_run_at: '2026-06-16T08:00:00Z' },
  },
  'GET /institutions': [{ id: INSTITUTION, name: 'Example Bank', logo_url: null, hide_below_balance: null }],
  'GET /assistant': {
    configured: true,
    is_enabled: true,
    base_url: 'http://localhost:8001/v1',
    model: 'sample-model',
    name: 'Assistant',
    has_key: false,
    allow_writes: true,
    apply_without_asking: false,
    tool_call_style: 'native',
    tools: [
      { name: 'list_transactions', description: 'Read transactions.', writes: false },
      { name: 'update_transaction', description: 'Change a transaction.', writes: true },
    ],
  },
}
