/** The autopay figure field, whose own emptiness blocks Save as much as a bad one does. */

import { describe, expect, it, vi } from 'vitest'

vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import type { BillConnection } from '@/lib/clients/bills'
import { renderScreen } from '@/test/renderScreen'

import { ConnectionDialog } from './ConnectionDialog'

const CONNECTION: BillConnection = {
  id: 'conn-power',
  biller: 'we-energies',
  label: 'Main account',
  username: 'somebody',
  site: '',
  credential_source: 'session',
  has_totp: false,
  second_factor: '',
  connected: true,
  signed_in_at: '2026-09-10T02:00:00Z',
  needs_sign_in: false,
  sign_in_paused: '',
  autopay_rule: 'days_before_due',
  autopay_days: null,
  autopay_day: null,
  autopay_account_id: null,
  pull_enabled: true,
  pull_at: null,
  last_pulled_at: '2026-09-17T09:00:00Z',
  last_pull_status: 'ok',
  last_pull_error: '',
  has_failure_screenshot: false,
  has_trail: false,
  can_retry_sign_in: false,
  pulling: false,
  created_at: '2026-09-01T00:00:00Z',
}

function render(connection: BillConnection): string {
  return renderScreen(<ConnectionDialog connection={connection} onClose={() => {}} />)
}

describe('ConnectionDialog autopay figure', () => {
  it('shows the error beside an empty figure a days-before rule needs, not just a bad one', () => {
    const html = render(CONNECTION)
    expect(html).toContain('Not a day')
    expect(html).toContain('disabled')
  })

  it('clears once the figure the rule needs is on file', () => {
    const html = render({ ...CONNECTION, autopay_days: 5 })
    expect(html).not.toContain('Not a day')
    expect(html).not.toContain('disabled')
  })
})
