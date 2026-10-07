import { ArrowLeftRight, ChevronRight } from 'lucide-react'
import { Fragment, useState } from 'react'

import { Money } from '@/components/Money'
import { Badge, Callout, EmptyState, Table, Td, Th } from '@/components/ui'
import {
  STATUS_LABELS,
  STATUS_TONES,
  foldPadding,
  groupEntries,
  type EntryGroup,
  type PlanBucket,
  type PlanEntry,
} from '@/lib/spendingPlan'
import { formatCount } from '@/lib/format'
import { sumMoney } from '@/lib/money'

/**
 * Bills keep their three families apart because their subtotals differ in
 * kind. Transfers and card payments net to zero by design: the spending was
 * counted when the purchase posted.
 */
const GROUP_LABELS: Record<EntryGroup, string> = {
  bill: 'Regular bills',
  subscription: 'Subscriptions',
  transfer: 'Transfers & credit card payments',
  goal: 'Goal contributions',
}

export interface PlanEntryPanelProps {
  bucket: PlanBucket
  /** The excluded list is read-only in a frozen month. */
  frozen: boolean
  onExclude: (entry: PlanEntry) => void
  onInclude: (entry: PlanEntry) => void
  /** Open the row: the transaction behind it, or the series and its history. */
  onOpen?: (entry: PlanEntry) => void
  emptyTitle: string
}

/**
 * One bucket's rows: what it counted, and what the user dropped this month,
 * which is the difference between the engine's figure and the one on screen.
 */
export function PlanEntryPanel({
  bucket,
  frozen,
  onExclude,
  onInclude,
  onOpen,
  emptyTitle,
}: PlanEntryPanelProps) {
  const [showExcluded, setShowExcluded] = useState(false)
  const groups = groupEntries(bucket.contributing)

  return (
    <div className="plan-entries">
      {bucket.contributing.length === 0 ? (
        <EmptyState
          compact
          title={emptyTitle}
          body="Nothing counted toward this bucket this month."
        />
      ) : (
        groups.map(([group, entries]) => (
          <section key={group ?? 'ungrouped'} className="plan-entries__group">
            {group === null ? (
              // The band is named even for a single group, so the counted rows
              // and the "Excluded this month" disclosure read as a pair. No
              // subtotal: it would repeat the panel header's.
              <header className="plan-entries__grouphead">
                <h4>Included this month</h4>
              </header>
            ) : (
              <header className="plan-entries__grouphead">
                <h4>{GROUP_LABELS[group]}</h4>
                {group === 'transfer' ? (
                  <span className="plan-entries__note hint hint--faint">
                    Listed for visibility. Nets to zero — the spending counted when the purchase
                    posted.
                  </span>
                ) : null}
                <Money value={sumMoney(entries.map((entry) => entry.amount))} />
              </header>
            )}
            <EntryTable
              entries={entries}
              frozen={frozen}
              onAct={onExclude}
              onOpen={onOpen}
              action="Exclude"
            />
          </section>
        ))
      )}

      <section className="plan-entries__excluded">
        <button
          type="button"
          className="plan-entries__disclosure"
          aria-expanded={showExcluded}
          onClick={() => setShowExcluded((open) => !open)}
        >
          <ChevronRight size={14} aria-hidden="true" data-open={showExcluded} />
          Excluded this month ({bucket.excluded.length})
        </button>
        {showExcluded ? (
          bucket.excluded.length === 0 ? (
            <EmptyState compact title="Nothing dropped from this bucket this month." />
          ) : (
            <EntryTable
              entries={bucket.excluded}
              frozen={frozen}
              onAct={onInclude}
              onOpen={onOpen}
              action="Include"
            />
          )
        ) : null}
      </section>

      {bucket.overwritten_amount === null ? null : (
        <Callout tone="warning">
          <p>
            The rows above add up to <Money value={bucket.calculated_amount} tone="neutral" />.
            This bucket shows <Money value={bucket.overwritten_amount} tone="neutral" /> because
            it is overridden.
          </p>
        </Callout>
      )}
    </div>
  )
}

/**
 * The rows. Each is pressable when the panel was given `onOpen`: the whole
 * row for a pointer, and the name — a real button — for the keyboard. The
 * action at the end stops the press from also opening the row. Padding rows
 * are folded into one line per name, which opens to list them.
 */
function EntryTable({
  entries,
  frozen,
  onAct,
  onOpen,
  action,
}: {
  entries: PlanEntry[]
  frozen: boolean
  onAct: (entry: PlanEntry) => void
  onOpen?: (entry: PlanEntry) => void
  action: string
}) {
  const [opened, setOpened] = useState<ReadonlySet<string>>(new Set())
  const toggle = (key: string) =>
    setOpened((current) => {
      const next = new Set(current)
      if (!next.delete(key)) next.add(key)
      return next
    })
  const row = (entry: PlanEntry, nested = false) => (
    <EntryRow
      key={entry.id}
      entry={entry}
      nested={nested}
      frozen={frozen}
      onAct={onAct}
      onOpen={onOpen}
      action={action}
    />
  )

  return (
    <Table density="sm" stack>
      <thead>
        <tr>
          <Th>Name</Th>
          <Th>Day</Th>
          <Th>Status</Th>
          <Th>Category</Th>
          <Th numeric>Amount</Th>
          <Th>
            <span className="visually-hidden">Actions</span>
          </Th>
        </tr>
      </thead>
      <tbody>
        {foldPadding(entries).map((line) => {
          if (line.kind === 'entry') return row(line.entry)
          const open = opened.has(line.key)
          return (
            <Fragment key={line.key}>
              <tr
                className="plan-entries__fold"
                data-clickable="true"
                onClick={() => toggle(line.key)}
              >
                <Td label="" className="plan-entries__lead">
                  <span className="plan-entries__name">
                    <button
                      type="button"
                      className="plan-entries__open"
                      aria-expanded={open}
                      onClick={(event) => {
                        event.stopPropagation()
                        toggle(line.key)
                      }}
                    >
                      <ChevronRight
                        size={12}
                        aria-hidden="true"
                        className="plan-entries__chevron"
                        data-open={open}
                      />
                      {line.name}
                    </button>
                    <span className="plan-entries__count hint hint--faint">
                      {formatCount(line.entries.length)} transactions
                    </span>
                  </span>
                </Td>
                <Td className="stack-inline nowrap">
                  <span className="plan-entries__day" />
                </Td>
                <Td className="stack-inline nowrap">
                  <Badge tone={STATUS_TONES[line.entries[0]!.status]}>
                    {STATUS_LABELS[line.entries[0]!.status]}
                  </Badge>
                </Td>
                <Td className="stack-inline nowrap">{line.category_name ?? 'Uncategorized'}</Td>
                <Td numeric className="stack-inline nowrap">
                  <Money value={line.amount} showPlus={line.amount > 0} />
                </Td>
                <Td label="" className="stack-corner" />
              </tr>
              {open ? line.entries.map((entry) => row(entry, true)) : null}
            </Fragment>
          )
        })}
      </tbody>
    </Table>
  )
}

function EntryRow({
  entry,
  nested,
  frozen,
  onAct,
  onOpen,
  action,
}: {
  entry: PlanEntry
  nested: boolean
  frozen: boolean
  onAct: (entry: PlanEntry) => void
  onOpen?: (entry: PlanEntry) => void
  action: string
}) {
  return (
    <tr
      className={nested ? 'plan-entries__nested' : undefined}
      data-clickable={onOpen ? 'true' : undefined}
      onClick={onOpen ? () => onOpen(entry) : undefined}
    >
      <Td label="" className="plan-entries__lead">
        <span className="plan-entries__name">
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
          {entry.is_transfer ? (
            <ArrowLeftRight size={12} aria-label="Transfer" className="plan-entries__glyph" />
          ) : null}
        </span>
      </Td>
      <Td className="stack-inline nowrap">
        <span className="plan-entries__day">{entry.due_on.slice(8, 10)}</span>
      </Td>
      <Td className="stack-inline nowrap">
        <Badge tone={STATUS_TONES[entry.status]}>{STATUS_LABELS[entry.status]}</Badge>
      </Td>
      <Td className="stack-inline nowrap">{entry.category_name ?? 'Uncategorized'}</Td>
      <Td numeric className="stack-inline nowrap">
        <Money value={entry.amount} showPlus={entry.amount > 0} />
      </Td>
      <Td label="" className="stack-corner">
        <button
          type="button"
          className="plan-entries__action"
          disabled={frozen}
          onClick={(event) => {
            event.stopPropagation()
            onAct(entry)
          }}
        >
          {action}
        </button>
      </Td>
    </tr>
  )
}
