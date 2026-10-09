/**
 * The notifications settings, rendered against a seeded cache. The catalog
 * invariants are tested in Go; this is the rendering half.
 */

import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'
import {
  ALERT_GROUPS,
  NOTIFICATIONS_KEY,
  PUSH_KEY,
  type AlertSetting,
  type AlertSettings,
} from '@/lib/clients/notifications'
import { renderScreen } from '@/test/renderScreen'
import { NotificationsSettings } from './NotificationsSettings'

function alert(overrides: Partial<AlertSetting> = {}): AlertSetting {
  return {
    type: 'large_transaction',
    label: 'Large transaction',
    trigger: 'Any transaction at or above this amount',
    group: 'spending',
    threshold: 'amount',
    channels: ['email', 'push', 'in_app'],
    evaluated: true,
    is_enabled: true,
    is_paused: false,
    channel_email: false,
    channel_push: false,
    channel_in_app: true,
    threshold_amount: moneyFromCents(50_000),
    threshold_count: null,
    threshold_percent: null,
    configured: true,
    ...overrides,
  }
}

function render(settings: AlertSettings): string {
  return renderScreen(<NotificationsSettings />, {
    seed: [
      [NOTIFICATIONS_KEY, settings],
      [PUSH_KEY, { enabled: false, subscribed: false }],
    ],
  })
}

const SETTINGS: AlertSettings = {
  alerts: [
    alert(),
    alert({
      type: 'credit_score_alert',
      label: 'Credit score: new alert',
      trigger: 'A new credit alert appears in your file',
      group: 'accounts',
      threshold: 'none',
      channels: ['email'],
      evaluated: false,
      threshold_amount: null,
    }),
  ],
  all_paused: false,
  email_enabled: false,
}

describe('the notifications settings', () => {
  it('draws every alert row with its trigger and offers pause-all', () => {
    const rendered = render(SETTINGS)
    expect(rendered).toContain('Large transaction')
    expect(rendered).toContain('Pause all')
  })

  it('prints a threshold as its value and a thresholdless alert as whenever', () => {
    const rendered = render({
      ...SETTINGS,
      alerts: [
        alert(),
        alert({ type: 'new_payee', label: 'New payee', threshold: 'none', threshold_amount: null }),
      ],
    })
    expect(rendered).toContain('500.00')
    expect(rendered).toContain('Whenever it happens')
  })

  it('leaves out an alert nothing evaluates yet', () => {
    const rendered = render(SETTINGS)
    expect(rendered).not.toContain('Credit score: new alert')
    expect(rendered).not.toContain('<details')
  })

  it('leaves out a group whose alerts nothing evaluates', () => {
    const rendered = render(SETTINGS)
    const accounts = ALERT_GROUPS.find((group) => group.id === 'accounts')
    expect(accounts?.hint).toBeTruthy()
    expect(rendered).not.toContain(accounts?.hint)
  })

  it('says the email column delivers nothing while no relay is set', () => {
    const rendered = render(SETTINGS)
    expect(rendered).toContain('SMTP_HOST')
  })

    // On a phone each checkbox carries its channel name, with "off" for a
    // channel this deployment cannot deliver on.
  it('stacks a row on a phone with every channel named beside its checkbox', () => {
    const rendered = render(SETTINGS)

    expect(rendered).toContain('table--stack')
    expect(rendered).toContain('data-label="Email · off"')
    expect(rendered).toContain('data-label="Push"')
    expect(rendered).toContain('data-label="In app"')
  })
})
