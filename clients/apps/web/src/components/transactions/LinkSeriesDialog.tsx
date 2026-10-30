/**
 * "Link to existing series", from a row. A slot another charge already pays
 * comes back as a refusal. The server picks the nearest slot, unless the series
 * has more than one open slot around the date, and then this asks which.
 */

import { Check, Repeat } from 'lucide-react'
import { useMemo, useState } from 'react'

import { Money } from '@/components/Money'
import {
  Button,
  DialogContent,
  EmptyState,
  FormDialog,
  SearchInput,
  SkeletonRows,
  Tabs,
  TabsList,
  TabsTrigger,
  useHeld,
} from '@/components/ui'
import {
  occurrenceKey,
  slotWindow,
  useAllSeries,
  useOccurrences,
  type Occurrence,
  type Series,
  type SeriesKind,
} from '@/lib/clients/upcoming'
import { accountNamer } from '@/lib/accounts'
import { formatDate } from '@/lib/format'
import { shortLabel } from '@/lib/recurrence'
import { displayPayee } from '@/lib/transactions/edits'
import type { Account, Transaction } from '@/lib/transactions/types'

type KindTab = 'all' | 'bills' | 'income' | 'transfers'

const KIND_TABS: readonly { id: KindTab; label: string; kinds: readonly SeriesKind[] | null }[] = [
  { id: 'all', label: 'All', kinds: null },
  { id: 'bills', label: 'Bills', kinds: ['bill', 'subscription'] },
  { id: 'income', label: 'Income', kinds: ['income'] },
  { id: 'transfers', label: 'Transfers', kinds: ['transfer', 'credit_card_payment'] },
]

interface LinkSeriesProps {
  accounts: readonly Account[]
  /** No `dueOn` means the server's nearest slot. */
  onLink: (txn: Transaction, series: Series, dueOn?: string) => void
  /** The "don't see it?" escape hatch into the series editor. */
  onCreateInstead: (txn: Transaction) => void
}

export function LinkSeriesDialog({
  txn: openTxn,
  onClose,
  ...rest
}: LinkSeriesProps & { txn: Transaction | null; onClose: () => void }) {
  const txn = useHeld(openTxn)
  return (
    <FormDialog open={openTxn !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      {txn === null ? null : <LinkSeriesForm txn={txn} {...rest} />}
    </FormDialog>
  )
}

function LinkSeriesForm({
  txn,
  accounts,
  onLink,
  onCreateInstead,
}: LinkSeriesProps & { txn: Transaction }) {
  const [tab, setTab] = useState<KindTab>('all')
  const [search, setSearch] = useState('')
  const [chosen, setChosen] = useState<Series | null>(null)
  const [slot, setSlot] = useState<string | null>(null)
  const list = useAllSeries(search)

  // Asked for while the picker is open rather than after a series is picked,
  // so the press either links or opens the slot step with no wait in between.
  const bounds = useMemo(() => slotWindow(txn.date), [txn.date])
  const slots = useOccurrences(bounds.from, bounds.to)
  const openSlotsOf = (series: Series) =>
    (slots.data?.items ?? []).filter((one) => one.series_id === series.id)

  const back = () => {
    setChosen(null)
    setSlot(null)
  }

  // One open slot, or a window not yet arrived, needs no question.
  const pick = (row: Transaction, series: Series) => {
    if (openSlotsOf(series).length > 1) {
      setChosen(series)
      return
    }
    onLink(row, series)
  }

  const kinds = KIND_TABS.find((one) => one.id === tab)?.kinds ?? null
  const rows = (list.data ?? []).filter(
    (one) => one.is_active && (kinds === null || kinds.includes(one.kind)),
  )
  const accountName = accountNamer(accounts)

  return (
    <DialogContent
      fills
      className="link-series"
      title="Link to a recurring item"
      description={
        <>
          {displayPayee(txn)} · <Money value={txn.amount} tone="flow" /> · {formatDate(txn.date)}
        </>
      }
    >
      {chosen !== null ? (
        <SlotPicker
          series={chosen}
          slots={openSlotsOf(chosen)}
          chosen={slot}
          onChoose={setSlot}
          onBack={back}
          onConfirm={() => onLink(txn, chosen, slot ?? undefined)}
        />
      ) : (
        <>
          <div className="link-series__filters">
            <Tabs
              value={tab}
              onValueChange={(next) => {
                const chosen = KIND_TABS.find((one) => one.id === next)
                if (chosen !== undefined) setTab(chosen.id)
              }}
            >
              <TabsList>
                {KIND_TABS.map((one) => (
                  <TabsTrigger key={one.id} value={one.id}>
                    {one.label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
            <SearchInput
              className="link-series__search"
              placeholder="Search recurring"
              aria-label="Search recurring"
              value={search}
              onChange={setSearch}
            />
          </div>

          {list.isPending ? (
            <div className="dialog__scroller">
              <SkeletonRows rows={5} />
            </div>
          ) : rows.length === 0 ? (
            <EmptyState compact className="dialog__scroller" title="Nothing recurring matches." />
          ) : (
            <ul className="pick-list dialog__scroller">
              {rows.map((one) => (
                <li key={one.id}>
                  <button type="button" className="pick-list__row" onClick={() => pick(txn, one)}>
                    <Repeat size={13} aria-hidden="true" />
                    <span className="pick-list__name">
                      {one.label}
                      <small>{shortLabel(one.recurrence)}</small>
                    </span>
                    <span className="pick-list__figures">
                      <Money value={one.amount} tone="flow" />
                      <small>{accountName(one.account_id)}</small>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}

          <p className="link-series__foot muted">
            Don&rsquo;t see it?{' '}
            <Button variant="ghost" size="sm" onClick={() => onCreateInstead(txn)}>
              Make it a new recurring item
            </Button>
          </p>
        </>
      )}
    </DialogContent>
  )
}

/** Which occurrence this charge pays, when the series has more than one open. */
function SlotPicker({
  series,
  slots,
  chosen,
  onChoose,
  onBack,
  onConfirm,
}: {
  series: Series
  slots: readonly Occurrence[]
  /** Null is "Nearest": the server's own choice, and what this opens on. */
  chosen: string | null
  onChoose: (dueOn: string | null) => void
  onBack: () => void
  onConfirm: () => void
}) {
  return (
    <>
      <p className="muted">
        {series.label} has more than one occurrence open around this date. Which one does this
        charge pay?
      </p>

      <ul className="pick-list dialog__scroller">
        <li>
          <button
            type="button"
            className="pick-list__row"
            aria-pressed={chosen === null}
            onClick={() => onChoose(null)}
          >
            {chosen === null ? (
              <Check size={13} aria-hidden="true" />
            ) : (
              <Repeat size={13} aria-hidden="true" />
            )}
            <span className="pick-list__name">
              Nearest
              <small>The occurrence closest to the charge&rsquo;s date</small>
            </span>
          </button>
        </li>
        {slots.map((one) => (
          <li key={occurrenceKey(one)}>
            <button
              type="button"
              className="pick-list__row"
              aria-pressed={chosen === one.due_on}
              onClick={() => onChoose(one.due_on)}
            >
              {chosen === one.due_on ? (
                <Check size={13} aria-hidden="true" />
              ) : (
                <Repeat size={13} aria-hidden="true" />
              )}
              <span className="pick-list__name">
                {formatDate(one.due_on)}
                <small>{one.status === 'past_due' ? 'Past due' : 'Upcoming'}</small>
              </span>
              <span className="pick-list__figures">
                <Money value={one.amount} tone="flow" />
                <small>expected</small>
              </span>
            </button>
          </li>
        ))}
      </ul>

      <p className="link-series__foot muted">
        <Button variant="ghost" size="sm" onClick={onBack}>
          Back
        </Button>
        <Button variant="primary" size="sm" onClick={onConfirm}>
          Link
        </Button>
      </p>
    </>
  )
}
