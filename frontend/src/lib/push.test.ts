import { afterEach, describe, expect, it, vi } from 'vitest'

import { PUSH_BLOCKER_TEXT, pushBlocker } from './push'

// Why a browser cannot subscribe: each refusal is reported distinctly, checked
// in an order that spends no permission prompt.

function browser(overrides: {
  secure?: boolean
  serviceWorker?: boolean
  push?: boolean
  permission?: NotificationPermission
}) {
  vi.stubGlobal('window', {
    isSecureContext: overrides.secure ?? true,
    ...(overrides.push === false ? {} : { PushManager: class {} }),
  })
  vi.stubGlobal(
    'navigator',
    overrides.serviceWorker === false ? {} : { serviceWorker: {} },
  )
  vi.stubGlobal('Notification', { permission: overrides.permission ?? 'default' })
}

afterEach(() => vi.unstubAllGlobals())

describe('pushBlocker', () => {
  it('reports an insecure origin first, before spending the permission prompt', () => {
    // A browser gives one prompt; checking the origin after asking wastes it.
    browser({ secure: false, permission: 'default' })
    expect(pushBlocker('key')).toBe('insecure-context')
    expect(PUSH_BLOCKER_TEXT['insecure-context']).toContain('HTTPS')
  })

  it('says the deployment is at fault, not the reader', () => {
    expect(PUSH_BLOCKER_TEXT['insecure-context']).toContain('plain HTTP')
    expect(PUSH_BLOCKER_TEXT['insecure-context']).toContain('everything else on this page still works')
  })

  it('distinguishes a blocked permission, which only the reader can undo', () => {
    browser({ permission: 'denied' })
    expect(pushBlocker('key')).toBe('denied')
    expect(PUSH_BLOCKER_TEXT.denied).toContain('site settings')
  })

  it('names a missing server keypair rather than blaming the browser', () => {
    browser({})
    expect(pushBlocker('')).toBe('no-key')
    expect(PUSH_BLOCKER_TEXT['no-key']).toContain('VAPID_PUBLIC_KEY')
  })

  it('reports an unsupported browser', () => {
    browser({ serviceWorker: false })
    expect(pushBlocker('key')).toBe('unsupported')
  })

  it('is null when everything is in place', () => {
    browser({})
    expect(pushBlocker('key')).toBeNull()
  })
})
