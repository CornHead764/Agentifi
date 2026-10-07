import { Monitor, Moon, Sun } from 'lucide-react'
import { useMemo } from 'react'
// The theme tiles are a bespoke control — a radio group that looks nothing like
// a radio — so they use the Radix primitive directly rather than `<Radio>`.
import { RadioGroup } from 'radix-ui'

import { InfoTip } from '@/components/InfoTip'
import { Card, Checkbox, OptionSelect, Switch } from '@/components/ui'
import { useMotion } from '@/contexts/motion'
import { usePrivacy } from '@/contexts/privacy'
import { isThemePreference, useTheme, type ThemePreference } from '@/contexts/theme'
import { currencyOptions } from '@/lib/currencies'
import { MOTION_SPEEDS, motionSpeedLabel } from '@/lib/motion'
import { RANGE_PRESETS, rangeChangeLabel } from '@/lib/dateRanges'
import { useCurrentSpace, useSavePreferences } from '@/lib/clients/spaces'
import { saveRoamingPreference, useRoamingPreferences } from '@/lib/preferences'
import { DEFAULT_TOAST_MS, TOAST_DURATIONS, toastDurationLabel } from '@/lib/toastDuration'
import {
  DEFAULT_SWIPE_LEFT,
  DEFAULT_SWIPE_RIGHT,
  SWIPE_ACTIONS,
  SWIPE_ACTION_LABELS,
  asSwipeAction,
  type SwipeAction,
} from '@/lib/transactions/gestures'
import { accountTypeLabel } from '@/lib/accountTypes'
import { useAccounts } from '@/lib/transactions/queries'
import { toggled } from '@/lib/toggle'

const THEMES: { value: ThemePreference; label: string; icon: typeof Sun }[] = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
]

/**
 * The display choices. The currency, the range and the sidebar belong to the
 * space; theme, privacy mode, animation speed, the toast duration and swipe
 * actions belong to the person. All save on change. The type list is built from the accounts the household has, not the
 * full taxonomy.
 */
export function DisplayPreferencesCard() {
  const { hidden, setHidden } = usePrivacy()
  const { preference, setPreference } = useTheme()
  const motion = useMotion()
  const { data: space } = useCurrentSpace()
  const accounts = useAccounts()
  const roaming = useRoamingPreferences()
  const save = useSavePreferences()

  const types = useMemo(() => {
    const seen = new Map<string, string>()
    for (const account of accounts.data ?? []) {
      if (account.is_closed) continue
      seen.set(account.type, accountTypeLabel(account.type))
    }
    return [...seen].sort((a, b) => a[1].localeCompare(b[1]))
  }, [accounts.data])

  // Null is "every type". It is expanded only for the checkboxes, so ticking
  // every box and never opening the screen look the same.
  const chosen = space?.sidebar_account_types ?? null
  const isListed = (type: string) => chosen === null || chosen.includes(type)

  function toggle(type: string, on: boolean) {
    const current = chosen ?? types.map(([value]) => value)
    save.mutate({ sidebar_account_types: toggled(current, type, on) })
  }

  return (
    <Card title="Display">
      <div className="setting-row setting-row--stacked">
        <div className="setting-row__text">
          <p className="setting-row__label">Theme</p>
          <p className="hint">Follows your account to other devices.</p>
        </div>
        <RadioGroup.Root
          className="theme-choice"
          value={preference}
          onValueChange={(value) => {
            if (isThemePreference(value)) setPreference(value)
          }}
          aria-label="Theme"
        >
          {THEMES.map(({ value, label, icon: Icon }) => (
            <RadioGroup.Item key={value} value={value} className="theme-choice__option">
              {label}
              <Icon size={16} />
            </RadioGroup.Item>
          ))}
        </RadioGroup.Root>
      </div>

      <div className="setting-row">
        <div className="setting-row__text">
          <p className="setting-row__label">Privacy mode</p>
          <p className="hint">Hides amounts; names and percentages stay.</p>
        </div>
        <Switch
          checked={hidden}
          onCheckedChange={setHidden}
          label={hidden ? 'Privacy mode on' : 'Privacy mode off'}
          labelPosition="before"
        />
      </div>

      <div className="setting-row">
        <div className="setting-row__text">
          <p className="setting-row__label">Primary currency</p>
          <p className="hint">
            Totals in this space are converted to it.{' '}
            <InfoTip>
              Changing it remakes every stored conversion from the rates on file. A day with no
              rate is left unconverted rather than guessed.
            </InfoTip>
          </p>
        </div>
        <OptionSelect
          value={space?.primary_currency ?? ''}
          onValueChange={(value) => save.mutate({ primary_currency: value })}
          disabled={!space}
          aria-label="Primary currency"
          options={currencyOptions(space?.primary_currency)}
        />
      </div>

      <div className="setting-row">
        <div className="setting-row__text">
          <p className="setting-row__label">Default date range</p>
          <p className="hint">
            Where dated pages start. The Spending Plan keeps its own.
          </p>
        </div>
        <OptionSelect
          value={space?.default_date_range || DEFAULT_RANGE}
          onValueChange={(value) =>
            save.mutate({ default_date_range: value === DEFAULT_RANGE ? null : value })
          }
          disabled={!space}
          aria-label="Default date range"
          options={[
            { value: DEFAULT_RANGE, label: 'Each page’s own default' },
            ...RANGE_PRESETS.map((preset) => ({
              value: preset,
              label: rangeChangeLabel(preset).replace(' change', ''),
            })),
          ]}
        />
      </div>

      {/* The speed goes through the motion context, because this device keeps
          a copy for the paint before the session loads. */}
      <div className="setting-row">
        <div className="setting-row__text">
          <p className="setting-row__label">Animation</p>
          <p className="hint">
            Sidebar, dialogs, menus and toasts. Reduced motion on the device means off.
          </p>
        </div>
        <OptionSelect
          value={String(motion.preference)}
          onValueChange={(value) => motion.setPreference(Number(value))}
          aria-label="Animation"
          // A duration the list does not offer must still show, or a save
          // would change it.
          options={(MOTION_SPEEDS.includes(motion.preference)
            ? MOTION_SPEEDS
            : [motion.preference, ...MOTION_SPEEDS]
          ).map((ms) => ({ value: String(ms), label: motionSpeedLabel(ms) }))}
        />
      </div>

      <div className="setting-row">
        <div className="setting-row__text">
          <p className="setting-row__label">Popups close after</p>
          <p className="hint">
            Pointing at one pauses it. Errors and running work stay until closed.
          </p>
        </div>
        <ToastDurationSelect
          value={roaming?.toastMs ?? DEFAULT_TOAST_MS}
          disabled={roaming === null}
        />
      </div>

      <div className="setting-row">
        <div className="setting-row__text">
          <p className="setting-row__label">Swipe a transaction left</p>
          <p className="hint">
            On a phone. Rows there have no menu button, so keep one swipe on Open menu.
          </p>
        </div>
        <SwipeSelect
          label="Swipe left"
          value={roaming?.swipeLeft ?? DEFAULT_SWIPE_LEFT}
          disabled={roaming === null}
          onChange={(action) => saveRoamingPreference({ swipe_left_action: action })}
        />
      </div>

      <div className="setting-row">
        <div className="setting-row__text">
          <p className="setting-row__label">Swipe a transaction right</p>
          <p className="hint">
            Marking reviewed also approves the assistant&rsquo;s proposal, if any.
          </p>
        </div>
        <SwipeSelect
          label="Swipe right"
          value={roaming?.swipeRight ?? DEFAULT_SWIPE_RIGHT}
          disabled={roaming === null}
          onChange={(action) => saveRoamingPreference({ swipe_right_action: action })}
        />
      </div>

      <div className="setting-row setting-row--stacked">
        <div className="setting-row__text">
          <p className="setting-row__label">Accounts in the sidebar</p>
          <p className="hint">
            Sidebar only; balances still count everywhere else.
          </p>
        </div>
        <div className="control-grid">
          {types.map(([type, label]) => (
            <Checkbox
              key={type}
              label={label}
              checked={isListed(type)}
              onCheckedChange={(state) => toggle(type, state === true)}
            />
          ))}
        </div>
      </div>
    </Card>
  )
}

/** Saved through the account, fire-and-forget, like the swipes. */
function ToastDurationSelect({ value, disabled }: { value: number; disabled: boolean }) {
  // A duration the list does not offer must still show, or a save would change it.
  const durations = TOAST_DURATIONS.includes(value) ? TOAST_DURATIONS : [value, ...TOAST_DURATIONS]
  return (
    <OptionSelect
      value={String(value)}
      onValueChange={(next) => saveRoamingPreference({ toast_duration_ms: Number(next) })}
      disabled={disabled}
      aria-label="Popups close after"
      options={durations.map((ms) => ({ value: String(ms), label: toastDurationLabel(ms) }))}
    />
  )
}

/** One direction of the register's swipe; saved through the account, fire-and-forget. */
function SwipeSelect({
  label,
  value,
  disabled,
  onChange,
}: {
  label: string
  value: SwipeAction
  disabled: boolean
  onChange: (action: SwipeAction) => void
}) {
  return (
    <OptionSelect
      value={value}
      onValueChange={(next) => onChange(asSwipeAction(next, value))}
      disabled={disabled}
      aria-label={label}
      options={SWIPE_ACTIONS.map((action) => ({ value: action, label: SWIPE_ACTION_LABELS[action] }))}
    />
  )
}

/** The sentinel for "no preference". Radix has no value for an empty option. */
const DEFAULT_RANGE = 'default'
