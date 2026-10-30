import { CircleHelp } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { Facts } from '@/components/Facts'
import { Money } from '@/components/Money'
import {
  Button,
  Callout,
  Card,
  EmptyState,
  Field,
  Input,
  MoneyInput,
  OptionSelect,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Table,
  Td,
  Th,
} from '@/components/ui'
import { formatCount, formatDate } from '@/lib/format'
import { reviewQueueLink } from '@/lib/transactions/links'
import type { Money as MoneyValue } from '@/lib/money'
import {
  PROJECTION_LABELS,
  capChildren,
  convertibleSlice,
  groupTinySlices,
  otherSpendExcludedRows,
  otherSpendRows,
  projectionMethod,
  sliceKey,
  withDirectChildren,
  type OtherSpendSlice,
  type PlanEntry,
  type ProjectionType,
  type SpendingPlanMonth,
  uncategorizedShare,
} from '@/lib/spendingPlan'

import { OtherSpendBubbles } from './OtherSpendBubbles'
import { absMoney, amountToWire, parseAmountInput } from '@/lib/money'

export interface OtherSpendPanelProps {
  month: SpendingPlanMonth
  frozen: boolean
  onProjectionChange: (type: ProjectionType, windowMonths: number) => void
  onBufferChange: (buffer: string) => void
  onAddToPlanned: (slice: OtherSpendSlice) => void
  /** Open a row: the transaction behind it and the ways to refile it. */
  onOpen?: (entry: PlanEntry) => void
}

/**
 * Everything actually spent that no bill and no envelope claimed. Actuals
 * only; the projection sits beside it, labelled with its method.
 *
 * The listed rows come from the bucket's own `contributing` list, never a
 * second query, which could disagree with the total over the bubble.
 */
export function OtherSpendPanel({
  month,
  frozen,
  onProjectionChange,
  onBufferChange,
  onAddToPlanned,
  onOpen,
}: OtherSpendPanelProps) {
  const total = month.other_spend_to_date
  const projection = month.projection
  const projected = month.projected_left

  const [openKey, setOpenKey] = useState<string | null>(null)
  const [selectedKey, setSelectedKey] = useState<string | null>(null)

  const top = useMemo(
    () => capChildren(withDirectChildren(groupTinySlices(month.other_spend_by_category))),
    [month.other_spend_by_category],
  )
  const open = openKey === null ? null : (top.find((one) => sliceKey(one) === openKey) ?? null)
  // A selection is a child of the open group, or a top-level bubble with no
  // children of its own — the two kinds of bubble that select rather than open.
  const selectable = open === null ? top : [...open.children, ...top]
  const selected = selectable.find((one) => sliceKey(one) === selectedKey) ?? null

  const entries = useMemo(
    () => otherSpendRows(month.buckets.other_spend, open, selected),
    [month.buckets.other_spend, open, selected],
  )
  const excludedEntries = useMemo(
    () => otherSpendExcludedRows(month.buckets.other_spend, open, selected),
    [month.buckets.other_spend, open, selected],
  )
  const convertible = convertibleSlice(open, selected)
  // A month led by "Uncategorized" says so in words and links to the fix.
  const unfiled = useMemo(
    () => uncategorizedShare(month.other_spend_by_category),
    [month.other_spend_by_category],
  )

  return (
    <div className="other-spend">
      {unfiled === null ? null : (
        <Callout tone="warning">
          <p>
            Uncategorized: <Money value={unfiled.amount} signs="absolute" tone="neutral" />.{' '}
            <Link to={reviewQueueLink({ uncategorized: true })}>
              Categorize {unfiled.count} {unfiled.count === 1 ? 'transaction' : 'transactions'}
            </Link>
          </p>
        </Callout>
      )}
      {top.length === 0 ? (
        <EmptyState compact title="Nothing spent outside bills and envelopes this month." />
      ) : (
        <figure className="other-spend__chart">
          {/* Above the chart: an open group's children glide outside the packed
              layout, onto anything below it. */}
          <div className="other-spend__actions">
            <Popover>
              <PopoverTrigger asChild>
                <Button size="sm" variant="ghost" className="other-spend__hint">
                  <CircleHelp size={14} aria-hidden="true" /> How to read this chart
                </Button>
              </PopoverTrigger>
              <PopoverContent align="start" side="bottom" className="other-spend__hint-pop">
                {open
                  ? `Inside ${open.category_name}. Press it again to close it, or a bubble to list its rows.`
                  : 'Press a bubble to open it. Sized by share of what has been spent so far.'}{' '}
                Actuals only — the projection is separate.
              </PopoverContent>
            </Popover>

            {convertible === null ? null : (
              <Button size="sm" disabled={frozen} onClick={() => onAddToPlanned(convertible)}>
                Add to Planned Spend
              </Button>
            )}
          </div>

          <OtherSpendBubbles
            slices={top}
            total={total}
            openKey={openKey}
            selected={selectedKey}
            onOpen={(slice: OtherSpendSlice | null) => {
              setOpenKey(slice === null ? null : sliceKey(slice))
              setSelectedKey(null)
            }}
            onSelect={(slice) => {
              if (slice === null) {
                setSelectedKey(null)
                return
              }
              // Selecting a top-level bubble closes the open group.
              if (top.some((one) => sliceKey(one) === sliceKey(slice))) setOpenKey(null)
              setSelectedKey(sliceKey(slice))
            }}
          />
        </figure>
      )}

      {top.length > 0 ? (
        <section className="other-spend__rows">
          <header className="other-spend__rows-head">
            <h4>
              {selected
                ? selected.category_name
                : open
                  ? open.category_name
                  : 'Everything in Other Spend'}
            </h4>
            <Money
              value={selected?.spent ?? open?.spent ?? total}
              signs="absolute"
              tone="neutral"
            />
          </header>
          {excludedEntries.length === 0 ? null : (
            <p className="plan-entries__note hint hint--faint other-spend__excluded-note">
              {formatCount(excludedEntries.length)} excluded (greyed, not in the total)
            </p>
          )}
          {entries.length + excludedEntries.length === 0 ? (
            <EmptyState compact title="No rows here." />
          ) : (
            <Table density="sm" stack>
              <thead>
                <tr>
                  <Th>Date</Th>
                  <Th>Payee</Th>
                  <Th>Category</Th>
                  <Th numeric>Amount</Th>
                </tr>
              </thead>
              <tbody>
                {entries.map((entry) => (
                  <tr
                    key={entry.id}
                    data-clickable={onOpen ? 'true' : undefined}
                    onClick={onOpen ? () => onOpen(entry) : undefined}
                  >
                    <Td label="" className="stack-lead nowrap">
                      {formatDate(entry.due_on)}
                    </Td>
                    <Td label="" className="stack-lead plan-entries__lead">
                      {onOpen ? (
                        <button
                          type="button"
                          className="plan-entries__open"
                          onClick={(event) => {
                            event.stopPropagation()
                            onOpen(entry)
                          }}
                        >
                          {entry.name}
                        </button>
                      ) : (
                        entry.name
                      )}
                    </Td>
                    <Td className="stack-inline nowrap">{entry.category_name ?? 'Uncategorized'}</Td>
                    <Td numeric label="" className="stack-corner nowrap">
                      <Money value={entry.amount} signs="absolute" tone="neutral" />
                    </Td>
                  </tr>
                ))}
                {excludedEntries.map((entry) => (
                  <tr
                    key={`excluded-${entry.id}`}
                    className="other-spend__row--excluded"
                    data-clickable={onOpen ? 'true' : undefined}
                    onClick={onOpen ? () => onOpen(entry) : undefined}
                  >
                    <Td label="" className="stack-lead nowrap">
                      {formatDate(entry.due_on)}
                    </Td>
                    <Td label="" className="stack-lead plan-entries__lead">
                      {onOpen ? (
                        <button
                          type="button"
                          className="plan-entries__open"
                          onClick={(event) => {
                            event.stopPropagation()
                            onOpen(entry)
                          }}
                        >
                          {entry.name}
                        </button>
                      ) : (
                        entry.name
                      )}
                    </Td>
                    <Td className="stack-inline nowrap">{entry.category_name ?? 'Uncategorized'}</Td>
                    <Td numeric label="" className="stack-corner nowrap">
                      <Money value={entry.amount} signs="absolute" tone="neutral" />
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </section>
      ) : null}

      <Card
        title="Projected Other Spend"
        actions={<Money value={month.projected_other_spending} signs="absolute" tone="neutral" />}
      >
        <Facts
          facts={[
            { label: 'Method', value: projectionMethod(projection, month) },
            { label: 'Day of month', value: month.days_elapsed },
            { label: 'Left at month end', value: <Money value={projected} /> },
          ]}
        />

        <div className="projection__controls">
          <Field label="Method">
            <OptionSelect
              value={projection.type}
              disabled={frozen}
              onValueChange={(next) =>
                onProjectionChange(asProjectionType(next), projection.window_months)
              }
              options={[
                { value: 'run_rate', label: PROJECTION_LABELS.run_rate },
                { value: 'prior_month', label: PROJECTION_LABELS.prior_month },
                { value: 'average_n_months', label: PROJECTION_LABELS.average_n_months },
              ]}
            />
          </Field>

          {projection.type === 'average_n_months' ? (
            <Field label="Months averaged">
              <Input
                type="number"
                min={1}
                max={24}
                numeric
                disabled={frozen}
                value={projection.window_months}
                onChange={(event) =>
                  onProjectionChange('average_n_months', Number(event.target.value))
                }
              />
            </Field>
          ) : null}

          <Field label="Buffer" hint="A flat add-on, for a method that reads optimistically.">
            <MoneyInput
              signed
              disabled={frozen}
              defaultValue={bufferInput(projection.buffer)}
              onBlur={(event) => {
                const wire = bufferEdit(event.target.value)
                if (wire === null) {
                  event.target.value = bufferInput(projection.buffer)
                  return
                }
                event.target.value = wire
                if (wire !== bufferInput(projection.buffer)) onBufferChange(wire)
              }}
            />
          </Field>
        </div>
      </Card>
    </div>
  )
}

/**
 * The buffer as a plain editable number. Not `formatMoney`: typing over
 * `$1,250.00` produces a string the server cannot parse as a `Decimal`.
 */
function bufferInput(buffer: MoneyValue): string {
  return amountToWire(absMoney(buffer))
}

/**
 * What a typed buffer sends, or null for a figure that is not one. An emptied
 * box clears to zero rather than posting the empty string the server refuses.
 */
function bufferEdit(typed: string): string | null {
  if (typed.trim() === '') return '0.00'
  return parseAmountInput(typed)?.wire ?? null
}

function asProjectionType(value: string): ProjectionType {
  if (value === 'prior_month' || value === 'average_n_months') return value
  return 'run_rate'
}
