import { useMemo, useState } from 'react'

import { MultiLine, labeledAxis } from '@/components/charts'
import { QueryBoundary } from '@/components/QueryBoundary'
import { ReminderList } from '@/components/ReminderList'
import { Card, Checkbox, Field, MoneyInput } from '@/components/ui'
import { accountTypeLabel } from '@/lib/accountTypes'
import { occurrenceMarkers, useCashFlow, type CashFlowLine } from '@/lib/clients/upcoming'
import { moneyToNumber, parseAmountInput } from '@/lib/money'
import { useAccounts } from '@/lib/transactions/queries'
import type { Horizon } from '@/lib/upcoming/horizon'
import { toggled } from '@/lib/toggle'

import { accountColors } from './accountColors'
import { LowBalanceNote } from './LowBalanceNote'

/** The accounts money is spent from: what the chart falls back to when nothing is scheduled. */
const CASH_TYPES = new Set(['checking', 'savings', 'credit_card', 'cash', 'money_market', 'cash_management'])

/**
 * Cash Flow: one line per selected account, from one expansion of the series
 * set. The account tree filters that set rather than querying, so unticking an
 * account cannot change the remaining lines.
 */
export function CashFlowTab({ horizon }: { horizon: Horizon }) {
  const [selected, setSelected] = useState<string[] | null>(null)
  // The low-balance line. A half-typed figure is not a threshold yet, so the
  // projection is asked for the parsed one or for none at all.
  const [warnBelow, setWarnBelow] = useState('')
  const threshold = parseAmountInput(warnBelow)
  const bounds = horizon
  const flow = useCashFlow(bounds.from, bounds.to, selected, threshold?.wire)
  const accounts = useAccounts()

  const lines = useMemo(() => flow.data?.accounts ?? [], [flow.data])
  // Until somebody picks, the lines are the accounts something is due to move
  // through in this window; every account would flatten the checking line
  // against the large balances. The tree still offers the rest.
  const chosen = useMemo(() => {
    if (selected !== null) return selected
    const moving = new Set((flow.data?.occurrences ?? []).map((one) => one.account_id))
    const withActivity = lines.filter((line) => moving.has(line.account_id))
    if (withActivity.length > 0) return withActivity.map((line) => line.account_id)
    const byId = new Map((accounts.data ?? []).map((one) => [one.id, one]))
    const cash = lines.filter((line) => CASH_TYPES.has(byId.get(line.account_id)?.type ?? ''))
    return (cash.length > 0 ? cash : lines).map((line) => line.account_id)
  }, [selected, flow.data, lines, accounts.data])

  const colorOf = useMemo(() => accountColors(lines), [lines])

  // Grouped by the account's own type label, which is the taxonomy the account
  // picker uses. The projection carries no group of its own.
  const groups = useMemo(() => {
    const byId = new Map((accounts.data ?? []).map((one) => [one.id, one]))
    const out = new Map<string, CashFlowLine[]>()
    for (const line of lines) {
      const type = byId.get(line.account_id)?.type
      const group = type === undefined ? 'Other' : accountTypeLabel(type)
      const bucket = out.get(group) ?? []
      bucket.push(line)
      out.set(group, bucket)
    }
    return [...out.entries()]
  }, [lines, accounts.data])

  return (
    // Full width, as on Overview: the projection runs the width of the page.
    <div className="stack">
      <Card>
        <QueryBoundary query={flow} rows={8}>
          {(data) => {
            const axis = uniqueDates(
              data.accounts.flatMap((line) => line.points.map((point) => point.on)),
            )
            return (
              <MultiLine
                axis={labeledAxis(axis)}
                series={data.accounts
                  .filter((line) => chosen.includes(line.account_id))
                  .map((line) => ({
                    key: line.account_id,
                    label: line.name,
                    color: colorOf(line.account_id),
                    dashed: true,
                    markers: occurrenceMarkers(line.account_id, data.occurrences, moneyToNumber),
                    points: Object.fromEntries(
                      line.points.map((point) => [point.on, moneyToNumber(point.balance)]),
                    ),
                  }))}
              />
            )
          }}
        </QueryBoundary>
      </Card>

      <div className="split split--cashflow">
        <Card title="Reminders" flush>
          <QueryBoundary query={flow} rows={6}>
            {(data) => (
              <ReminderList
                occurrences={data.occurrences.filter(
                  (one) => one.account_id !== null && chosen.includes(one.account_id),
                )}
              />
            )}
          </QueryBoundary>
        </Card>

        <Card title="Accounts" subtitle={`${chosen.length} of ${lines.length} selected`}>
          <Field
            label="Warn me below"
            hint="The first day each line is projected to dip under this."
          >
            <MoneyInput
              placeholder="0.00"
              value={warnBelow}
              onChange={(event) => setWarnBelow(event.target.value)}
            />
          </Field>

          {groups.map(([group, members]) => (
            <section key={group} className="acct-tree">
              <Checkbox
                label={group}
                checked={members.every((line) => chosen.includes(line.account_id))}
                onCheckedChange={(checked) => {
                  const ids = members.map((line) => line.account_id)
                  setSelected(
                    checked === true
                      ? [...new Set([...chosen, ...ids])]
                      : chosen.filter((id) => !ids.includes(id)),
                  )
                }}
              />
              <ul>
                {members.map((line) => (
                  <li key={line.account_id}>
                    <span className="dot" style={{ background: colorOf(line.account_id) }} />
                    <Checkbox
                      label={line.name}
                      checked={chosen.includes(line.account_id)}
                      onCheckedChange={(checked) =>
                        setSelected(toggled(chosen, line.account_id, checked === true))
                      }
                    />
                    <LowBalanceNote line={line} warned={threshold !== null} />
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </Card>
      </div>
    </div>
  )
}

function uniqueDates(dates: readonly string[]): string[] {
  return [...new Set(dates)].sort()
}
