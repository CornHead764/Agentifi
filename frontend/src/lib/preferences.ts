/**
 * Theme, privacy mode, animation speed, swipe actions and the toast duration,
 * roaming between devices. localStorage is the fast copy for first paint and
 * the account row the true one; swipe actions and the toast duration have only
 * the account copy. A module store, not
 * a context, because the theme and privacy providers sit above the auth
 * provider. The providers adopt a server value once per account, when the
 * session first loads; after that the device wins, so nothing oscillates.
 */

import { useSyncExternalStore } from 'react'

import {
  DEFAULT_SWIPE_LEFT,
  DEFAULT_SWIPE_RIGHT,
  asSwipeAction,
  type SwipeAction,
} from './transactions/gestures'
import { asMotionMs } from './motion'
import { asToastMs } from './toastDuration'
import { api } from './api'
import { createListenerSet } from './listenerSet'

export interface RoamingPreferences {
  /** The providers adopt once per id. */
  userId: string
  /** Unvalidated; the provider knows its own set. */
  theme: string
  privacyMode: boolean
  /** Narrowed, so a name from an older or newer client falls back to the default. */
  swipeLeft: SwipeAction
  swipeRight: SwipeAction
  /** Milliseconds, narrowed because it is written into a stylesheet. Zero means no animation. */
  animationMs: number
  /** Milliseconds a toast stays before it dismisses itself. */
  toastMs: number
}

export interface PreferencePatch {
  theme?: string
  privacy_mode?: boolean
  locale?: string
  swipe_left_action?: SwipeAction
  swipe_right_action?: SwipeAction
  animation_duration_ms?: number
  toast_duration_ms?: number
}

export interface UserPreferenceFields {
  id: string
  theme: string
  privacy_mode: boolean
  swipe_left_action?: string
  swipe_right_action?: string
  animation_duration_ms?: number
  toast_duration_ms?: number
}

let current: RoamingPreferences | null = null
const listeners = createListenerSet()
const failureListeners = createListenerSet<[unknown]>()

function same(a: RoamingPreferences | null, b: RoamingPreferences | null): boolean {
  if (a === null || b === null) return a === b
  return (
    a.userId === b.userId &&
    a.theme === b.theme &&
    a.privacyMode === b.privacyMode &&
    a.swipeLeft === b.swipeLeft &&
    a.swipeRight === b.swipeRight &&
    a.animationMs === b.animationMs &&
    a.toastMs === b.toastMs
  )
}

export function publishRoamingPreferences(next: RoamingPreferences | null): void {
  if (same(current, next)) return
  current = next
  listeners.notify()
}

export function roamingPreferences(): RoamingPreferences | null {
  return current
}

export function subscribeRoamingPreferences(listener: () => void): () => void {
  return listeners.subscribe(listener)
}

/** The same getter serves the server snapshot, which only the test suite asks for. */
export function useRoamingPreferences(): RoamingPreferences | null {
  return useSyncExternalStore(
    subscribeRoamingPreferences,
    roamingPreferences,
    roamingPreferences,
  )
}

export function onPreferenceSaveFailure(listener: (error: unknown) => void): () => void {
  return failureListeners.subscribe(listener)
}

/**
 * Fire-and-forget: the change is already applied on this device. A failure
 * reaches `onPreferenceSaveFailure`. Nothing is sent while signed out.
 */
export function saveRoamingPreference(patch: PreferencePatch): void {
  if (current === null) return
  void api.patch<UserPreferenceFields>('/auth/me', patch).then(
    (user) => publishRoamingPreferences(fromUserRecord(user)),
    (error: unknown) => failureListeners.notify(error),
  )
}

export function fromUserRecord(user: UserPreferenceFields): RoamingPreferences {
  return {
    userId: user.id,
    theme: user.theme,
    privacyMode: user.privacy_mode,
    swipeLeft: asSwipeAction(user.swipe_left_action, DEFAULT_SWIPE_LEFT),
    swipeRight: asSwipeAction(user.swipe_right_action, DEFAULT_SWIPE_RIGHT),
    animationMs: asMotionMs(user.animation_duration_ms),
    toastMs: asToastMs(user.toast_duration_ms),
  }
}

/** Test seam: drop every subscriber and forget what was published. */
export function resetRoamingPreferences(): void {
  current = null
  listeners.clear()
  failureListeners.clear()
}
