/**
 * Notification settings. The server answers every read and write with the
 * whole catalog, defaults filled in; nothing here holds a default of its own.
 * Writes replace the cache rather than invalidating it, so a refetch cannot
 * overwrite a threshold being typed.
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, ApiError, type MoneyShape } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { amountToWire, type Money } from '@/lib/money'
import type { Uuid } from '@/lib/transactions/types'

export type ThresholdKind = 'none' | 'amount' | 'count' | 'percent'

export type AlertChannel = 'email' | 'push' | 'in_app'

export type AlertGroup = 'spending' | 'accounts' | 'bills' | 'planning' | 'summaries'

export interface AlertSetting {
  type: string
  label: string
  trigger: string
  group: AlertGroup
  threshold: ThresholdKind
  /** Never draw a toggle outside these. */
  channels: AlertChannel[]
  /** False while nothing yet decides that this alert has fired. */
  evaluated: boolean

  is_enabled: boolean
  is_paused: boolean
  channel_email: boolean
  channel_push: boolean
  channel_in_app: boolean

  threshold_amount: Money | null
  threshold_count: number | null
  /** Decimal string, "10" is ten percent. Not money; never rounded to cents. */
  threshold_percent: string | null

  /** False while this row is still the catalog's default. */
  configured: boolean
}

export interface AlertSettings {
  alerts: AlertSetting[]
  all_paused: boolean
  /** Whether this deployment can deliver on each channel. */
  email_enabled: boolean
  push_enabled: boolean
}

const SETTINGS_SHAPE: MoneyShape<AlertSettings> = {
  alerts: { threshold_amount: 'money' },
}

export interface NotificationCard {
  id: Uuid
  alert_type: string
  label: string
  title: string
  body: string
  url: string
  read_at: string | null
  created_at: string
}

export interface NotificationFeed {
  notifications: NotificationCard[]
  /** The whole unread count, not the length of this page. */
  unread: number
}

const FEED_KEY = ['notifications', 'feed'] as const
export const NOTIFICATIONS_KEY = ['notifications', 'settings'] as const

export function useNotificationFeed(unreadOnly = false) {
  return useQuery({
    // Under FEED_KEY so one invalidation reaches both lists.
    queryKey: [...FEED_KEY, unreadOnly],
    queryFn: ({ signal }) =>
      api.get<NotificationFeed>(
        unreadOnly ? '/notifications?unread=true' : '/notifications',
        undefined,
        signal,
      ),
    // The sweep runs after a background sync, so the bell has to poll.
    refetchInterval: 60_000,
  })
}

export function useMarkNotificationRead() {
  return useInvalidatingMutation(
    (id: Uuid) =>
      api.post<void>(`/notifications/${id}/read`).catch((error: unknown) => {
        // Already-read is a 404; the card ends up read either way.
        if (error instanceof ApiError && error.status === 404) return
        throw error
      }),
    [FEED_KEY],
    { failure: 'That notification was not marked read' },
  )
}

export function useMarkAllNotificationsRead() {
  return useInvalidatingMutation(() => api.post<void>('/notifications/read'), [FEED_KEY], {
    failure: 'The notifications were not marked read',
  })
}

/** Takes one card off the feed for good; a cleared card counts as read. */
export function useClearNotification() {
  return useInvalidatingMutation(
    (id: Uuid) =>
      api.delete<void>(`/notifications/${id}`).catch((error: unknown) => {
        // Already cleared is a 404; the card is gone either way.
        if (error instanceof ApiError && error.status === 404) return
        throw error
      }),
    [FEED_KEY],
    { failure: 'That notification was not cleared' },
  )
}

export function useClearAllNotifications() {
  return useInvalidatingMutation(() => api.delete<void>('/notifications'), [FEED_KEY], {
    failure: 'The notifications were not cleared',
  })
}

export function useAlertSettings() {
  return useQuery({
    queryKey: NOTIFICATIONS_KEY,
    queryFn: ({ signal }) =>
      api.get<AlertSettings>('/notifications/settings', SETTINGS_SHAPE, signal),
  })
}

/** Whole-row. */
export interface AlertSettingUpdate {
  is_enabled: boolean
  channel_email: boolean
  channel_push: boolean
  channel_in_app: boolean
  threshold_amount?: string | null
  threshold_count?: number | null
  threshold_percent?: string | null
}

export function useSaveAlertSetting() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ type, update }: { type: string; update: AlertSettingUpdate }) =>
      api.put<AlertSettings>(`/notifications/settings/${type}`, update, SETTINGS_SHAPE),
    onSuccess: (settings) => client.setQueryData(NOTIFICATIONS_KEY, settings),
  })
}

export function usePauseAllAlerts() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (paused: boolean) =>
      api.post<AlertSettings>('/notifications/settings/pause', { paused }, SETTINGS_SHAPE),
    onSuccess: (settings) => client.setQueryData(NOTIFICATIONS_KEY, settings),
  })
}

export const ALERT_GROUPS: { id: AlertGroup; label: string; hint: string }[] = [
  {
    id: 'spending',
    label: 'Spending',
    hint: 'What is leaving the accounts, as it happens.',
  },
  {
    id: 'accounts',
    label: 'Accounts',
    hint: 'Balances crossing a line you set.',
  },
  { id: 'bills', label: 'Bills & income', hint: 'What is due, and what has arrived.' },
  { id: 'planning', label: 'Plan & goals', hint: 'Limits and targets you set for yourself.' },
  { id: 'summaries', label: 'Summaries', hint: 'Periodic round-ups rather than events.' },
]

export const CHANNELS: readonly AlertChannel[] = ['email', 'push', 'in_app']

export const CHANNEL_LABELS: Record<AlertChannel, string> = {
  email: 'Email',
  push: 'Push',
  in_app: 'In app',
}

export function thresholdValue(alert: AlertSetting): string {
  switch (alert.threshold) {
    case 'amount':
      return alert.threshold_amount === null ? '' : amountToWire(alert.threshold_amount)
    case 'percent':
      return alert.threshold_percent === null ? '' : trimZeros(alert.threshold_percent)
    case 'count':
      return alert.threshold_count === null ? '' : String(alert.threshold_count)
    default:
      return ''
  }
}

/** In text rather than through a float. */
function trimZeros(value: string): string {
  return value.includes('.') ? value.replace(/\.?0+$/, '') : value
}

export interface AlertEdit {
  channel_email?: boolean
  channel_push?: boolean
  channel_in_app?: boolean
  /** As typed, never through a float. */
  threshold?: string
}

/** Carries only the threshold field this alert has; the server refuses any other. */
export function updateFrom(alert: AlertSetting, edit: AlertEdit): AlertSettingUpdate {
  const email = edit.channel_email ?? alert.channel_email
  const push = edit.channel_push ?? alert.channel_push
  const inApp = edit.channel_in_app ?? alert.channel_in_app
  const threshold = edit.threshold ?? thresholdValue(alert)

  const update: AlertSettingUpdate = {
    // Derived, so it cannot disagree with the channels.
    is_enabled: email || push || inApp,
    channel_email: email,
    channel_push: push,
    channel_in_app: inApp,
  }
  switch (alert.threshold) {
    case 'amount':
      update.threshold_amount = threshold
      break
    case 'percent':
      update.threshold_percent = threshold
      break
    case 'count':
      update.threshold_count = Number.parseInt(threshold, 10)
      break
  }
  return update
}

/* --- Web push: a subscription is per browser, not per account. ----------- */

export interface PushSubscriptionRow {
  id: Uuid
  user_agent: string
  created_at: string
  last_used_at: string | null
}

export interface PushSubscriptions {
  /** False when this deployment has no VAPID keypair. */
  enabled: boolean
  /** The VAPID public key; public by design. */
  public_key: string
  subscriptions: PushSubscriptionRow[]
}

export const PUSH_KEY = ['notifications', 'push'] as const

export function usePushSubscriptions() {
  return useQuery({
    queryKey: PUSH_KEY,
    queryFn: ({ signal }) => api.get<PushSubscriptions>('/notifications/push', undefined, signal),
  })
}

export function useSubscribeToPush() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (keys: { endpoint: string; p256dh: string; auth: string }) =>
      api.post<PushSubscriptions>('/notifications/push', keys),
    onSuccess: (rows) => client.setQueryData(PUSH_KEY, rows),
  })
}

export function useForgetPushBrowser() {
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/notifications/push/${id}`),
    [PUSH_KEY],
    { failure: 'That browser was not forgotten' },
  )
}
