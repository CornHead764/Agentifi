/**
 * Subscribing this browser to pushed alerts. Each refusal is kept distinct
 * because each has a different fix: the deployment or the person.
 */

import { base64urlToBuffer, bufferToBase64url } from './base64url'

export type PushBlocker =
  | 'insecure-context'
  | 'unsupported'
  | 'denied'
  | 'dismissed'

export interface PushSubscriptionKeys {
  endpoint: string
  p256dh: string
  auth: string
}

/** Checked before prompting: a browser gives one permission prompt. */
export function pushBlocker(): PushBlocker | null {
  if (typeof window === 'undefined') return 'unsupported'
  if (!window.isSecureContext) return 'insecure-context'
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) return 'unsupported'
  if (typeof Notification !== 'undefined' && Notification.permission === 'denied') return 'denied'
  return null
}

export function isPushBlocker(reason: unknown): reason is PushBlocker {
  return typeof reason === 'string' && reason in PUSH_BLOCKER_TEXT
}

export const PUSH_BLOCKER_TEXT: Record<PushBlocker, string> = {
  'insecure-context':
    'Push notifications need HTTPS. This install is reached over plain HTTP, so the browser ' +
    'will not allow them — everything else on this page still works.',
  unsupported: 'This browser does not support push notifications.',
  denied:
    'Notifications are blocked for this site. Allow them in the browser’s site settings, then ' +
    'try again — the page cannot ask a second time.',
  dismissed: 'The permission prompt was dismissed, so nothing was changed.',
}

function encodeKey(buffer: ArrayBuffer | null): string {
  if (buffer === null) return ''
  return bufferToBase64url(buffer)
}

/** Throws a PushBlocker rather than an Error; the caller has a sentence for each. */
export async function subscribeThisBrowser(publicKey: string): Promise<PushSubscriptionKeys> {
  const blocker = pushBlocker()
  if (blocker !== null) throw blocker

  const permission = await Notification.requestPermission()
  if (permission === 'denied') throw 'denied' satisfies PushBlocker
  if (permission !== 'granted') throw 'dismissed' satisfies PushBlocker

  const registration = await navigator.serviceWorker.register('/sw.js')
  await navigator.serviceWorker.ready

  const subscription = await registration.pushManager.subscribe({
    // Chrome refuses a subscription that is not user-visible.
    userVisibleOnly: true,
    // `applicationServerKey` is typed against a plain ArrayBuffer, not the view.
    applicationServerKey: base64urlToBuffer(publicKey),
  })
  return {
    endpoint: subscription.endpoint,
    p256dh: encodeKey(subscription.getKey('p256dh')),
    auth: encodeKey(subscription.getKey('auth')),
  }
}
