/** What the shell reads on every page: the space, the bell, the connection and the dashboard. */

import { USER_ID } from './auth'
import { BROKER, CONNECTION, CREDIT_UNION, INSTITUTION, id } from './core'

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

/** Every panel the dashboard offers, on, in the order the demo shows them. */
const DASHBOARD = [
  'net_worth',
  'spending_plan',
  'bills',
  'review',
  'top_categories',
  'goals',
  'recent',
  'investments',
  'watchlist',
  'spending',
  'income',
  'savings_rate',
  'planned_spend',
  'holdings',
].map((panel) => ({ id: panel, on: true }))

export const SHELL = {
  'GET /spaces': [SPACE],
  'GET /spaces/current': SPACE,
  'GET /spaces/invitations': [],
  'GET /spaces/current/dashboard': { layout: DASHBOARD },
  'GET /setup-guide': {
    dismissed: true,
    complete: true,
    skipped: [],
    steps: [
      { id: 'import', state: 'done', optional: false },
      { id: 'connect', state: 'done', optional: false },
      { id: 'match', state: 'done', optional: false },
      { id: 'assistant', state: 'done', optional: true },
      { id: 'bills', state: 'done', optional: true },
      { id: 'backups', state: 'done', optional: true },
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
  ],
  'GET /notifications': {
    notifications: [
      {
        id: id('note', 1),
        alert_type: 'bill_due',
        label: 'Bill due',
        title: 'Example Medical Group bill due in 4 days',
        body: '$85.00, paid by hand.',
        url: '/upcoming',
        read_at: null,
        created_at: '2026-06-15T12:00:00Z',
      },
      {
        id: id('note', 2),
        alert_type: 'large_transaction',
        label: 'Large transaction',
        title: 'A $480.00 charge at Example Airlines',
        body: 'On Sample Rewards Visa.',
        url: '/transactions',
        read_at: '2026-06-03T15:00:00Z',
        created_at: '2026-06-02T15:00:00Z',
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
        last_sync_at: '2026-06-15T08:00:00Z',
        last_successful_sync_at: '2026-06-15T08:00:00Z',
        retry_not_before: null,
        created_at: '2025-01-01T00:00:00Z',
        sync: null,
      },
    ],
    schedule: { enabled: true, at: '04:00', time_zone: 'America/New_York', next_run_at: '2026-06-16T08:00:00Z' },
  },
  'GET /institutions': [
    { id: INSTITUTION, name: 'Example Bank', logo_url: null, hide_below_balance: null },
    { id: CREDIT_UNION, name: 'Sample Credit Union', logo_url: null, hide_below_balance: null },
    { id: BROKER, name: 'Example Brokerage', logo_url: null, hide_below_balance: null },
  ],
}
