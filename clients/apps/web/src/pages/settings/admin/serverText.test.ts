import { describe, expect, it } from 'vitest'

import {
  draftValue,
  serverSettingsBody,
  type ServerSetting,
  type UpdateStatus,
} from '@/lib/clients/admin'

import { canReset, describeUpdate, groupSettings, settingNote, shortCommit } from './serverText'

function setting(overrides: Partial<ServerSetting> = {}): ServerSetting {
  return {
    key: 'SYNC_AT',
    group: 'Banks and sync',
    label: 'Daily sync time',
    help: 'In the server’s time zone.',
    kind: 'time',
    value: '04:00',
    default: '04:00',
    source: 'default',
    live: false,
    pending_restart: false,
    ...overrides,
  }
}

describe('saving the server settings', () => {
  const saved = setting({ key: 'SYNC_AT', value: '05:00', source: 'database' })
  const fromEnvironment = setting({ key: 'FRONTEND_URL', kind: 'text', value: 'https://a.example', source: 'environment' })
  const untouched = setting({ key: 'SYNC_CHECK_MINUTES', kind: 'number', value: '5', default: '5' })
  const settings = [saved, fromEnvironment, untouched]

  it('sends what is saved, and nothing it was not told', () => {
    expect(serverSettingsBody(settings, {})).toEqual({ values: { SYNC_AT: '05:00' } })
  })

  it('sends an edit, and drops a setting going back to its default', () => {
    expect(serverSettingsBody(settings, { SYNC_CHECK_MINUTES: ' 7 ' })).toEqual({
      values: { SYNC_AT: '05:00', SYNC_CHECK_MINUTES: '7' },
    })
    expect(serverSettingsBody(settings, { SYNC_AT: null })).toEqual({ values: {} })
  })

  it('never sends a setting the environment sets', () => {
    expect(serverSettingsBody(settings, { FRONTEND_URL: 'https://b.example' }).values).not.toHaveProperty(
      'FRONTEND_URL',
    )
  })

  it('shows the default for a setting going back to it', () => {
    expect(draftValue(saved, {})).toBe('05:00')
    expect(draftValue(saved, { SYNC_AT: null })).toBe('04:00')
    expect(draftValue(saved, { SYNC_AT: '06:30' })).toBe('06:30')
  })
})

describe('what a setting says about itself', () => {
  it('names the variable that sets it from the environment, and offers no reset', () => {
    const fromEnvironment = setting({ source: 'environment' })
    expect(settingNote(fromEnvironment, {})).toContain('Set by SYNC_AT in the environment or .env')
    expect(canReset(fromEnvironment, {})).toBe(false)
  })

  it('says when a setting applies only after a restart', () => {
    expect(settingNote(setting(), {})).toContain('Applies when the server next starts.')
    expect(settingNote(setting({ live: true }), {})).not.toContain('next starts')
  })

  it('offers the default for a saved or edited setting only', () => {
    expect(canReset(setting({ source: 'database' }), {})).toBe(true)
    expect(canReset(setting(), { SYNC_AT: '05:00' })).toBe(true)
    expect(canReset(setting(), {})).toBe(false)
    expect(canReset(setting({ source: 'database' }), { SYNC_AT: null })).toBe(false)
  })

  it('keeps the server’s order within and across groups', () => {
    const groups = groupSettings([
      setting({ key: 'A', group: 'One' }),
      setting({ key: 'B', group: 'Two' }),
      setting({ key: 'C', group: 'One' }),
    ])
    expect(groups.map(([group, members]) => [group, members.map((one) => one.key)])).toEqual([
      ['One', ['A', 'C']],
      ['Two', ['B']],
    ])
  })
})

describe('the running build', () => {
  it('shortens a commit and keeps a local build’s suffix', () => {
    expect(shortCommit('0f3c9a51b2d7e8c4a6f1093d5b7e2c8a4f6d1b30')).toBe('0f3c9a51b2d7')
    expect(shortCommit('0f3c9a51b2d7e8c4a6f1093d5b7e2c8a4f6d1b30-dirty')).toBe('0f3c9a51b2d7-dirty')
  })
})

describe('describeUpdate', () => {
  const status = (overrides: Partial<UpdateStatus> = {}): UpdateStatus => ({
    enabled: true,
    current_version: '0.2.0',
    latest_version: 'v0.2.0',
    update_available: false,
    url: 'https://github.com/CornHead764/Agentifi/releases/tag/v0.2.0',
    checked_at: '2026-10-08T12:00:00Z',
    error: '',
    ...overrides,
  })

  it('names a newer release', () => {
    expect(describeUpdate(status({ latest_version: 'v0.3.0', update_available: true }))).toBe(
      'v0.3.0 is out',
    )
  })

  it('says the running build is current', () => {
    expect(describeUpdate(status())).toBe('v0.2.0, up to date')
  })

  it('does not call an unversioned build up to date', () => {
    expect(describeUpdate(status({ current_version: '' }))).toBe(
      'v0.2.0; this build does not know its own version',
    )
  })

  it('says why a first check failed', () => {
    expect(describeUpdate(status({ latest_version: '', error: 'rate limited' }))).toBe(
      'Could not check: rate limited',
    )
  })

  it('says when the check is off', () => {
    expect(describeUpdate(status({ enabled: false, latest_version: '' }))).toBe(
      'Not checked: the update check is off',
    )
  })
})
