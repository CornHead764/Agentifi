import { describe, expect, it } from 'vitest'

import { moneyFromCents } from '@/lib/money'

import { thresholdValue, type AlertSetting } from './notifications'

function alert(overrides: Partial<AlertSetting>): AlertSetting {
  return {
    type: 'large_transaction',
    label: 'Large transaction',
    trigger: '',
    group: 'spending',
    threshold: 'amount',
    channels: ['in_app'],
    evaluated: true,
    is_enabled: true,
    is_paused: false,
    channel_email: false,
    channel_push: false,
    channel_in_app: true,
    threshold_amount: null,
    threshold_count: null,
    threshold_percent: null,
    configured: false,
    ...overrides,
  }
}

describe('thresholdValue', () => {
  it('shows an amount as the text a person would type', () => {
    expect(thresholdValue(alert({ threshold_amount: moneyFromCents(25_000) }))).toBe('250.00')
  })

  it('reads a percentage as the decimal the server sent, not as money', () => {
    expect(thresholdValue(alert({ threshold: 'percent', threshold_percent: '10.00' }))).toBe('10')
    expect(thresholdValue(alert({ threshold: 'percent', threshold_percent: '12.50' }))).toBe('12.5')
    expect(thresholdValue(alert({ threshold: 'percent', threshold_percent: '100' }))).toBe('100')
  })
})
