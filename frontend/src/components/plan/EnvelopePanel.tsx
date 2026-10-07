import { Eraser, LayoutGrid, List, ListOrdered, Pencil, RotateCcw, Tags, Undo2 } from 'lucide-react'
import { useMemo } from 'react'

import { EnvelopeBar } from '@/components/plan/EnvelopeBar'
import { Money } from '@/components/Money'
import {
  Card,
  EmptyState,
  IconButton,
  OptionSelect,
  OverflowMenu,
  RowActions,
  Table,
  Td,
  Th,
} from '@/components/ui'
import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { bucketSubject } from '@/lib/assistant/subjects'
import {
  envelopeAvailable,
  envelopePctUsed,
  envelopeState,
  type Envelope,
} from '@/lib/spendingPlan'

export type EnvelopeSort = 'name' | 'available' | 'used'
export type EnvelopeView = 'list' | 'cards'

export interface EnvelopePanelProps {
  envelopes: Envelope[]
  frozen: boolean
  /**
   * The month on screen, as the plan stores it: the plan is materialized per
   * month, so a question about an envelope must name its month.
   */
  month: string
  view: EnvelopeView
  sort: EnvelopeSort
  onReleaseRollover: (envelope: Envelope) => void
  onChangeRollover: (envelope: Envelope) => void
  onEditTarget: (envelope: Envelope) => void
  /** Drop this month's target override, restoring the envelope's own target. */
  onClearTarget: (envelope: Envelope) => void
  onEditCategories: (envelope: Envelope) => void
  /** Open the rows the envelope claimed this month. */
  onShowTransactions?: (envelope: Envelope) => void
}

const AVAILABLE_LABELS: Record<ReturnType<typeof envelopeState>, string> = {
  normal: 'Available to spend',
  with_rollover: 'Available with rollover',
  overspent: 'Overspent',
}

/** The sort and the list or card switch, for the header of the card holding the envelopes. */
export function EnvelopeControls({
  view,
  sort,
  onView,
  onSort,
}: {
  view: EnvelopeView
  sort: EnvelopeSort
  onView: (view: EnvelopeView) => void
  onSort: (sort: EnvelopeSort) => void
}) {
  return (
    <>
      <OptionSelect
        value={sort}
        onValueChange={(next) => onSort(asSort(next))}
        aria-label="Sort envelopes"
        className="envelopes__sort"
        options={[
          { value: 'name', label: 'Name' },
          { value: 'available', label: 'Available' },
          { value: 'used', label: 'Percent used' },
        ]}
      />
      <IconButton
        label="List view"
        variant={view === 'list' ? 'secondary' : 'ghost'}
        size="sm"
        onClick={() => onView('list')}
      >
        <List size={14} />
      </IconButton>
      <IconButton
        label="Card view"
        variant={view === 'cards' ? 'secondary' : 'ghost'}
        size="sm"
        onClick={() => onView('cards')}
      >
        <LayoutGrid size={14} />
      </IconButton>
    </>
  )
}

/**
 * Planned spend: one envelope per row, list or card. The readouts are balances,
 * so amounts are neutral and the state carries the colour; overspent is coral.
 */
export function EnvelopePanel({ envelopes, view, sort, ...handlers }: EnvelopePanelProps) {
  const ordered = useMemo(() => sortEnvelopes(envelopes, sort), [envelopes, sort])

  if (ordered.length === 0) {
    return (
      <EmptyState
        compact
        title="No planned expenses yet"
        body="Give categories a monthly target. Their spending leaves Other Spend."
      />
    )
  }

  if (view === 'cards') {
    return (
      <div className="envelopes__grid">
        {ordered.map((envelope) => (
          <Card
            key={envelope.id}
            title={<EnvelopeName envelope={envelope} {...handlers} />}
            subtitle={<EnvelopeCategories envelope={envelope} />}
            actions={<EnvelopeMenu envelope={envelope} {...handlers} />}
          >
            <div className="stack stack--3">
              <EnvelopeBar envelope={envelope} />
              <EnvelopeReadout envelope={envelope} />
            </div>
          </Card>
        ))}
      </div>
    )
  }

  return (
    <Table lines={2} stack>
      <thead>
        <tr>
          <Th>Expense</Th>
          <Th>Spent</Th>
          <Th numeric>Available</Th>
          <Th>
            <span className="visually-hidden">Actions</span>
          </Th>
        </tr>
      </thead>
      <tbody>
        {ordered.map((envelope) => (
          <tr key={envelope.id}>
            <Td label="" className="envelope__lead">
              <span className="envelope__name">
                <EnvelopeName envelope={envelope} {...handlers} />
              </span>
              <span className="cell__sub envelope__categories">
                <EnvelopeCategories envelope={envelope} />
              </span>
            </Td>
            <Td label="" className="envelope__bar">
              <EnvelopeBar envelope={envelope} />
            </Td>
            <Td numeric className="stack-inline nowrap">
              <EnvelopeReadout envelope={envelope} />
            </Td>
            <Td label="" className="stack-corner">
              <EnvelopeMenu envelope={envelope} {...handlers} />
            </Td>
          </tr>
        ))}
      </tbody>
    </Table>
  )
}

type Handlers = Omit<EnvelopePanelProps, 'envelopes' | 'view' | 'sort'>

function EnvelopeName({
  envelope,
  onShowTransactions,
}: { envelope: Envelope } & Pick<Handlers, 'onShowTransactions'>) {
  if (!onShowTransactions) return <>{envelope.name}</>
  return (
    <button
      type="button"
      className="plan-entries__open"
      onClick={() => onShowTransactions(envelope)}
    >
      {envelope.name}
    </button>
  )
}

function EnvelopeCategories({ envelope }: { envelope: Envelope }) {
  const count = envelope.txn_ids.length
  return (
    <>
      {count} {count === 1 ? 'transaction' : 'transactions'} in{' '}
      {envelope.categories.map((category) => category.name).join(', ')}
    </>
  )
}

function EnvelopeReadout({ envelope }: { envelope: Envelope }) {
  const state = envelopeState(envelope)
  return (
    <span className="envelope__readout" data-state={state}>
      <Money
        value={envelopeAvailable(envelope)}
        signs="absolute"
        tone={state === 'overspent' ? 'signed' : 'neutral'}
      />
      <span className="cell__sub envelope__state">
        {AVAILABLE_LABELS[state]}
        {envelope.auto_release_rollover ? ' · auto-releases' : ''}
      </span>
    </span>
  )
}

function EnvelopeMenu({
  envelope,
  frozen,
  month,
  onReleaseRollover,
  onChangeRollover,
  onEditTarget,
  onClearTarget,
  onEditCategories,
  onShowTransactions,
}: { envelope: Envelope } & Handlers) {
  return (
    <RowActions>
      <OverflowMenu
        label={`Actions for ${envelope.name}`}
        actions={[
          <AskMenuItem
            subject={() =>
              bucketSubject({
                id: envelope.id,
                name: envelope.name,
                month,
                amount: envelope.spent,
              })
            }
          />,
          onShowTransactions
            ? {
                label: 'Show transactions…',
                icon: <ListOrdered size={14} />,
                onSelect: () => onShowTransactions(envelope),
              }
            : null,
          {
            label: 'Release unspent funds',
            icon: <Undo2 size={14} />,
            disabled: frozen || envelope.rollover_amount === 0,
            onSelect: () => onReleaseRollover(envelope),
          },
          {
            label: "Edit this month's expense…",
            icon: <Pencil size={14} />,
            disabled: frozen,
            onSelect: () => onEditTarget(envelope),
          },
          // Without this a typed override could never be taken off.
          {
            label: "Clear this month's override",
            icon: <Eraser size={14} />,
            disabled: frozen || envelope.overwritten_target_amount === null,
            onSelect: () => onClearTarget(envelope),
          },
          {
            label: 'Change rollover amount…',
            icon: <RotateCcw size={14} />,
            disabled: frozen,
            onSelect: () => onChangeRollover(envelope),
          },
          // The only item here that outlives the month: the other
          // three are overrides on this one.
          {
            label: 'Edit expense series…',
            icon: <Tags size={14} />,
            disabled: frozen,
            onSelect: () => onEditCategories(envelope),
          },
        ]}
      />
    </RowActions>
  )
}

function sortEnvelopes(envelopes: Envelope[], sort: EnvelopeSort): Envelope[] {
  const ordered = [...envelopes]
  switch (sort) {
    case 'available':
      return ordered.sort((a, b) => envelopeAvailable(a) - envelopeAvailable(b))
    case 'used':
      return ordered.sort((a, b) => envelopePctUsed(b) - envelopePctUsed(a))
    default:
      return ordered.sort((a, b) => a.name.localeCompare(b.name))
  }
}

function asSort(value: string): EnvelopeSort {
  if (value === 'available' || value === 'used') return value
  return 'name'
}
