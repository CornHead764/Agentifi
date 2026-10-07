import { Field, Input, OptionSelect, Switch } from '@/components/ui'
import type { AutopayRule } from '@/lib/clients/bills'

import { switchedAutopay } from './draft'

/** The rules in the order a household thinks of them, least to most specific. */
const AUTOPAY_KINDS: readonly (readonly [Exclude<AutopayRule, 'none'>, string])[] = [
  ['on_due_date', 'On the due date'],
  ['days_before_due', 'A few days before due'],
  ['day_of_month', 'On a day of the month'],
]

function asAutopayRule(value: string): AutopayRule {
  const found = AUTOPAY_KINDS.find(([one]) => one === value)
  return found ? found[0] : 'on_due_date'
}

/**
 * Whether the provider takes its bills by itself, and when. The switch is the
 * rule's `none`, not a flag beside it.
 */
export function AutopayControls({
  kind,
  figure,
  invalid,
  onKind,
  onFigure,
}: {
  kind: AutopayRule
  figure: string
  /** The figure the rule needs is not a day. */
  invalid: boolean
  onKind: (kind: AutopayRule) => void
  onFigure: (figure: string) => void
}) {
  const on = kind !== 'none'
  return (
    <>
      <div className="setting-row">
        <div className="setting-row__text">
          <p className="setting-row__label">Autopay</p>
          <p className="hint">
            {on
              ? 'The provider takes each bill by itself.'
              : 'Off: each account’s newest unpaid statement is a reminder to pay it yourself.'}
          </p>
        </div>
        <Switch
          checked={on}
          onCheckedChange={(next) => onKind(switchedAutopay(next))}
          label="Autopay"
          labelPosition="before"
        />
      </div>
      {on ? (
        <div className="form-row">
          <Field label="Pays">
            <OptionSelect
              value={kind}
              onValueChange={(next) => onKind(asAutopayRule(next))}
              options={AUTOPAY_KINDS.map(([value, label]) => ({ value, label }))}
            />
          </Field>
          {kind === 'days_before_due' || kind === 'day_of_month' ? (
            <Field
              label={kind === 'days_before_due' ? 'Days before' : 'Day of the month'}
              error={invalid ? 'Not a day' : undefined}
            >
              <Input
                numeric
                inputMode="numeric"
                value={figure}
                onChange={(event) => onFigure(event.target.value)}
                placeholder={kind === 'days_before_due' ? '5' : '9'}
              />
            </Field>
          ) : null}
        </div>
      ) : null}
    </>
  )
}
