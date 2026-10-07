import { BellOff, BellRing, MailWarning } from 'lucide-react'
import { useState, type ChangeEvent, type KeyboardEvent } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Button,
  Callout,
  Card,
  Checkbox,
  ConfirmDialog,
  EmptyState,
  Input,
  List,
  ListRow,
  MoneyInput,
  PageHeader,
  Switch,
  Table,
  Td,
  Th,
  useConfirm,
} from '@/components/ui'
import {
  ALERT_GROUPS,
  CHANNEL_LABELS,
  CHANNELS,
  thresholdValue,
  updateFrom,
  useAlertSettings,
  useForgetPushBrowser,
  usePauseAllAlerts,
  usePushSubscriptions,
  useSubscribeToPush,
  useSaveAlertSetting,
  type AlertChannel,
  type AlertEdit,
  type AlertSetting,
  type AlertSettings,
  type PushSubscriptionRow,
} from '@/lib/clients/notifications'
import { PUSH_BLOCKER_TEXT, isPushBlocker, pushBlocker, subscribeThisBrowser } from '@/lib/push'
import { formatTimestamp } from '@/lib/format'
import { ZERO_MONEY, parseAmountInput, parseWholeNumber } from '@/lib/money'

/**
 * The alert catalog, less the alerts nothing evaluates yet. A channel an alert
 * cannot use is not drawn at all. Pause all is held apart from the per-alert
 * switches, so lifting it restores what was chosen.
 */
export function NotificationsSettings() {
  const settings = useAlertSettings()

  const save = useSaveAlertSetting()
  const pauseAll = usePauseAllAlerts()

  return (
    <>
      <PageHeader
        title="Notifications"
        subtitle="Nothing is emailed or pushed until you turn it on."
        actions={
          settings.data ? (
            <label className="pause-all">
              <span>Pause all</span>
              <Switch
                checked={settings.data.all_paused}
                disabled={pauseAll.isPending}
                onCheckedChange={(checked) => pauseAll.mutate(checked === true)}
                aria-label="Pause all notifications"
              />
            </label>
          ) : null
        }
      />
      <QueryBoundary query={settings} rows={6}>
        {(data) => (
          <>
            {data.email_enabled ? null : (
              <Callout tone="warning" icon={<MailWarning size={14} />}>
                No mail server, so Email delivers nothing. Set <code>SMTP_HOST</code> to turn it
                on.
              </Callout>
            )}
            {data.all_paused ? (
              <Callout icon={<BellOff size={14} />}>
                Everything is paused. Your choices below return when you lift it.
              </Callout>
            ) : null}

            <PushCard />

            {ALERT_GROUPS.map((group) => {
              const alerts = data.alerts.filter(
                (alert) => alert.group === group.id && alert.evaluated,
              )
              if (alerts.length === 0) return null
              return (
                <Card key={group.id} title={group.label} subtitle={group.hint} flush>
                  <AlertTable
                    alerts={alerts}
                    undeliverable={undeliverable(data)}
                    pending={save.isPending}
                    onChange={(alert, edit) =>
                      save.mutate({ type: alert.type, update: updateFrom(alert, edit) })
                    }
                  />
                </Card>
              )
            })}
          </>
        )}
      </QueryBoundary>
    </>
  )
}

/**
 * Channels this deployment cannot deliver on. Their checkboxes stay usable (the
 * choice takes effect once a relay is configured), but the header says so.
 */
function undeliverable(data: AlertSettings): Set<AlertChannel> {
  const out = new Set<AlertChannel>()
  if (!data.email_enabled) out.add('email')
  if (!data.push_enabled) out.add('push')
  return out
}

/** The browsers allowed to receive a pushed alert. Every refusal is reported as itself. */
function PushCard() {
  const rows = usePushSubscriptions()
  const subscribe = useSubscribeToPush()
  const forget = useConfirm(useForgetPushBrowser(), {
    variables: (browser: PushSubscriptionRow) => browser.id,
  })
  const [refusal, setRefusal] = useState<string | null>(null)

  const data = rows.data
  const blocker = data ? pushBlocker(data.public_key) : null

  const allow = () => {
    setRefusal(null)
    subscribeThisBrowser(data?.public_key ?? '')
      .then((keys) => subscribe.mutate(keys))
      .catch((reason: unknown) => {
        setRefusal(
          isPushBlocker(reason) ? PUSH_BLOCKER_TEXT[reason] : 'The browser refused to subscribe.',
        )
      })
  }

  return (
    <Card
      title="Push notifications"
      subtitle="Alerts on this device, even with the app closed. One entry per browser."
      actions={
        <Button size="sm" disabled={blocker !== null || subscribe.isPending} onClick={allow}>
          <BellRing size={13} aria-hidden="true" />
          {subscribe.isPending ? 'Allowing…' : 'Allow on this browser'}
        </Button>
      }
    >
      {blocker !== null ? <Callout tone="warning">{PUSH_BLOCKER_TEXT[blocker]}</Callout> : null}
      {refusal !== null ? <Callout tone="expense">{refusal}</Callout> : null}

      {(data?.subscriptions ?? []).length > 0 ? (
        <List>
          {data?.subscriptions.map((one) => (
            <ListRow
              key={one.id}
              title={<span title={one.user_agent}>{browserLabel(one.user_agent)}</span>}
              sub={`added ${formatTimestamp(one.created_at, 'date')}`}
              actions={
                <Button size="sm" onClick={() => forget.ask(one)}>
                  Forget
                </Button>
              }
            />
          ))}
        </List>
      ) : (
        <EmptyState compact title="No browser receives alerts yet." />
      )}

      <ConfirmDialog
        {...forget.dialog}
        title={
          forget.target
            ? `Stop notifications to ${browserLabel(forget.target.user_agent)}?`
            : 'Stop notifications to this browser?'
        }
        description="No more alerts there until it is allowed again from that browser."
        confirmLabel="Forget"
      />
    </Card>
  )
}

/** One group's rows. */
function AlertTable({
  alerts,
  undeliverable,
  pending,
  onChange,
}: {
  alerts: AlertSetting[]
  undeliverable: Set<AlertChannel>
  pending: boolean
  onChange: (alert: AlertSetting, edit: AlertEdit) => void
}) {
  return (
    <Table density="sm" lines={2} stack className="alerts">
      <thead>
        <tr>
          <Th>Alert</Th>
          <Th>When</Th>
          {CHANNELS.map((channel) => (
            <Th key={channel} numeric>
              {CHANNEL_LABELS[channel]}
              {undeliverable.has(channel) ? <span className="muted"> · off</span> : null}
            </Th>
          ))}
        </tr>
      </thead>
      <tbody>
        {alerts.map((alert) => (
          <AlertRow
            key={alert.type}
            alert={alert}
            undeliverable={undeliverable}
            pending={pending}
            onChange={(edit) => onChange(alert, edit)}
          />
        ))}
      </tbody>
    </Table>
  )
}

function AlertRow({
  alert,
  undeliverable,
  pending,
  onChange,
}: {
  alert: AlertSetting
  undeliverable: Set<AlertChannel>
  pending: boolean
  onChange: (edit: AlertEdit) => void
}) {
  return (
    <tr>
      <Td label="">
        {alert.label}
        <span className="cell__sub cell__clip" title={alert.trigger}>
          {alert.trigger}
        </span>
      </Td>
      <Td label="" className="alerts__when">
        <ThresholdInput alert={alert} pending={pending} onChange={onChange} />
      </Td>
      {CHANNELS.map((channel) => (
        <Td
          key={channel}
          numeric
          className="stack-inline"
          label={`${CHANNEL_LABELS[channel]}${undeliverable.has(channel) ? ' · off' : ''}`}
        >
          {alert.channels.includes(channel) ? (
            <Checkbox
              checked={channelOn(alert, channel)}
              className={undeliverable.has(channel) ? 'checkbox--inert' : undefined}
              disabled={pending || alert.is_paused}
              aria-label={`${CHANNEL_LABELS[channel]} for ${alert.label}`}
              onCheckedChange={(checked) =>
                onChange({ [channelField(channel)]: checked === true })
              }
            />
          ) : (
            <span className="muted" aria-label={`${alert.label} cannot use ${CHANNEL_LABELS[channel]}`}>
              —
            </span>
          )}
        </Td>
      ))}
    </tr>
  )
}

/**
 * The threshold input, or plain text when the alert compares nothing. Saved on
 * blur, since each write returns the whole catalog. An invalid value is
 * refused with a message and the typing is kept.
 */
function ThresholdInput({
  alert,
  pending,
  onChange,
}: {
  alert: AlertSetting
  pending: boolean
  onChange: (edit: AlertEdit) => void
}) {
  const [draft, setDraft] = useState<string | null>(null)
  const [problem, setProblem] = useState<string | null>(null)
  const value = draft ?? thresholdValue(alert)

  if (alert.threshold === 'none') return <span className="muted">Whenever it happens</span>

  const commit = () => {
    if (draft === null) return
    const cleaned = draft.trim()
    const read = cleaned === '' ? null : readThreshold(alert.threshold, cleaned)
    if (read !== null && 'problem' in read) {
      setProblem(read.problem)
      return
    }
    setDraft(null)
    setProblem(null)
    if (read !== null && read.value !== thresholdValue(alert)) onChange({ threshold: read.value })
  }

  const field = {
    value,
    disabled: pending || alert.is_paused,
    'aria-label': `Threshold for ${alert.label}`,
    'aria-invalid': problem ? true : undefined,
    onChange: (event: ChangeEvent<HTMLInputElement>) => {
      setDraft(event.target.value)
      setProblem(null)
    },
    onBlur: commit,
    onKeyDown: (event: KeyboardEvent<HTMLInputElement>) => {
      if (event.key === 'Enter') event.currentTarget.blur()
    },
  }

  return (
    <span className="threshold">
      {alert.threshold === 'amount' ? (
        <MoneyInput size="sm" {...field} />
      ) : (
        <Input size="sm" inputMode="decimal" {...field} />
      )}
      {alert.threshold === 'percent' ? <span className="muted">%</span> : null}
      {alert.threshold === 'count' ? <span className="muted">or more</span> : null}
      {problem ? (
        <span className="field__error" role="alert">
          {problem}
        </span>
      ) : null}
    </span>
  )
}

/**
 * A typed threshold as the value saved, or why it cannot be. Checked here too
 * because a rejected save would repaint the field from the stored value.
 */
function readThreshold(
  kind: AlertSetting['threshold'],
  typed: string,
): { value: string } | { problem: string } {
  if (kind === 'count') {
    const count = parseWholeNumber(typed)
    return count !== null && count >= 1
      ? { value: String(count) }
      : { problem: 'A whole number, 1 or more' }
  }
  if (kind === 'amount') {
    const amount = parseAmountInput(typed)
    return amount !== null && amount.cents >= ZERO_MONEY
      ? { value: amount.wire }
      : { problem: 'An amount like 250 or 1,250.00' }
  }
  return /^\d+(\.\d{1,2})?$/.test(typed)
    ? { value: typed }
    : { problem: 'A percentage like 80 or 82.5 — no % sign' }
}

function channelField(channel: AlertChannel) {
  return channel === 'in_app' ? 'channel_in_app' : channel === 'push' ? 'channel_push' : 'channel_email'
}

function channelOn(alert: AlertSetting, channel: AlertChannel): boolean {
  if (channel === 'email') return alert.channel_email
  if (channel === 'push') return alert.channel_push
  return alert.channel_in_app
}

/** "Firefox on Linux" rather than the whole user-agent string. */
function browserLabel(userAgent: string): string {
  const browser = /Edg\//.test(userAgent)
    ? 'Edge'
    : /Firefox\//.test(userAgent)
      ? 'Firefox'
      : /Chrome\//.test(userAgent)
        ? 'Chrome'
        : /Safari\//.test(userAgent)
          ? 'Safari'
          : null
  const system = /iPhone|iPad/.test(userAgent)
    ? 'iOS'
    : /Android/.test(userAgent)
      ? 'Android'
      : /Mac OS X|Macintosh/.test(userAgent)
        ? 'macOS'
        : /Windows/.test(userAgent)
          ? 'Windows'
          : /Linux|X11/.test(userAgent)
            ? 'Linux'
            : null
  if (browser === null) return userAgent === '' ? 'Unknown browser' : userAgent
  return system === null ? browser : `${browser} on ${system}`
}
