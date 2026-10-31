/** Publishing the same values twice wakes nobody, so a `/auth/me` refetch cannot flip a device back. */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const patch = vi.fn()

vi.mock('./api', () => ({ api: { patch: (...args: unknown[]) => patch(...args) } }))

import {
  onPreferenceSaveFailure,
  publishRoamingPreferences,
  resetRoamingPreferences,
  roamingPreferences,
  saveRoamingPreference,
  subscribeRoamingPreferences,
  type RoamingPreferences,
} from './preferences'

const ADA: RoamingPreferences = {
  userId: 'u-ada',
  theme: 'light',
  privacyMode: false,
  swipeLeft: 'menu',
  swipeRight: 'review',
  animationMs: 160,
  toastMs: 6000,
}

beforeEach(() => {
  resetRoamingPreferences()
  patch.mockReset()
  patch.mockResolvedValue({ id: 'u-ada', theme: 'light', privacy_mode: false })
})

afterEach(() => resetRoamingPreferences())

describe('publishing what the account holds', () => {
  it('tells subscribers once, and not again for the same values', () => {
    const heard = vi.fn()
    subscribeRoamingPreferences(heard)

    publishRoamingPreferences(ADA)
    publishRoamingPreferences({ ...ADA })
    expect(heard).toHaveBeenCalledTimes(1)
    expect(roamingPreferences()).toEqual(ADA)

    publishRoamingPreferences({ ...ADA, theme: 'dark' })
    expect(heard).toHaveBeenCalledTimes(2)
  })

  it('goes back to nothing when the session ends', () => {
    publishRoamingPreferences(ADA)
    publishRoamingPreferences(null)
    expect(roamingPreferences()).toBeNull()
  })
})

describe('saving a preference', () => {
  // A missing or unknown name falls back to the direction's default.
  it('takes the direction defaults when the record does not carry them', async () => {
    publishRoamingPreferences(ADA)
    patch.mockResolvedValue({ id: 'u-ada', theme: 'light', privacy_mode: false })
    saveRoamingPreference({ theme: 'light' })
    await vi.waitFor(() => expect(roamingPreferences()).toEqual(ADA))
  })

  it('sends only the field that changed', async () => {
    publishRoamingPreferences(ADA)
    saveRoamingPreference({ theme: 'dark' })
    expect(patch).toHaveBeenCalledWith('/auth/me', { theme: 'dark' })
  })

  it('adopts what the server answered, so nothing is left disagreeing', async () => {
    publishRoamingPreferences(ADA)
    patch.mockResolvedValue({
      id: 'u-ada',
      theme: 'dark',
      privacy_mode: true,
      swipe_left_action: 'flag',
      swipe_right_action: 'review',
    })
    saveRoamingPreference({ theme: 'dark' })
    await vi.waitFor(() =>
      expect(roamingPreferences()).toEqual({
        ...ADA,
        theme: 'dark',
        privacyMode: true,
        swipeLeft: 'flag',
      }),
    )
  })

  it('sends nothing while signed out — the login screen has a theme switch too', () => {
    saveRoamingPreference({ theme: 'dark' })
    expect(patch).not.toHaveBeenCalled()
  })

  it('announces a refusal instead of losing it', async () => {
    const failed = vi.fn()
    onPreferenceSaveFailure(failed)
    publishRoamingPreferences(ADA)
    patch.mockRejectedValue(new Error('nope'))

    saveRoamingPreference({ privacy_mode: true })
    await vi.waitFor(() => expect(failed).toHaveBeenCalledTimes(1))
    // The device keeps the change either way; only the roaming half failed.
    expect(roamingPreferences()).toEqual(ADA)
  })
})
