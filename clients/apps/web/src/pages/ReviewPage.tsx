import { CircleCheck } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'

import { Button, EmptyState, PageHeader, SkeletonRows, useToast } from '@/components/ui'
import {
  looksRight,
  useApproveRows,
  useFileRow,
  useReviewDays,
  useReviewItems,
  useReviewSummary,
  useUndoReview,
  type ReviewDay,
  type ReviewRow,
} from '@/lib/clients/review'
import { useCategories } from '@/lib/transactions/queries'
import { plural } from '@agentifi/core/format'

import { DaySection, type RowHandlers } from './review/DaySection'
import { ItemBand } from './review/ItemBand'
import { RowSheet } from './review/RowSheet'
import { badgeSentence, dayLabel } from './review/words'

/**
 * Review: what needs a person, items first, then the new transactions by
 * day, the way a statement is checked. calculations.md §15.
 */
export function ReviewPage() {
  const summary = useReviewSummary()
  const items = useReviewItems()
  const days = useReviewDays()
  const categories = useCategories()
  const approve = useApproveRows()
  const file = useFileRow()
  const undo = useUndoReview()
  const toast = useToast()

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [pickingId, setPickingId] = useState<string | null>(null)
  const [openRow, setOpenRow] = useState<ReviewRow | null>(null)
  // Approved here and not yet gone from a refetch: drawn ticked, skipped by the keys.
  const [approvedIds, setApprovedIds] = useState<ReadonlySet<string>>(new Set())

  const allDays = useMemo(() => days.data?.pages.flatMap((page) => page.days) ?? [], [days.data])
  const rows = useMemo(
    () => allDays.flatMap((day) => day.rows).filter((row) => !approvedIds.has(row.transaction_id)),
    [allDays, approvedIds],
  )

  const markApproved = (ids: readonly string[], on: boolean) =>
    setApprovedIds((current) => {
      const next = new Set(current)
      for (const id of ids) {
        if (on) next.add(id)
        else next.delete(id)
      }
      return next
    })

  const offerUndo = useCallback(
    (title: string, token: string, ids: readonly string[]) => {
      toast.show({
        title,
        tone: 'success',
        action: {
          label: 'Undo',
          onSelect: () => undo.mutate(token, { onSuccess: () => markApproved(ids, false) }),
        },
      })
    },
    [toast, undo],
  )

  /** The next open row after this one, or the one before it at the end. */
  const neighbour = useCallback(
    (id: string) => {
      const index = rows.findIndex((row) => row.transaction_id === id)
      return rows[index + 1] ?? rows[index - 1] ?? null
    },
    [rows],
  )

  const approveRow = useCallback(
    (row: ReviewRow) => {
      if (row.reason === 'possible_duplicate') return
      if (row.reason === 'no_category') {
        setPickingId(row.transaction_id)
        return
      }
      setSelectedId(neighbour(row.transaction_id)?.transaction_id ?? null)
      markApproved([row.transaction_id], true)
      approve.mutate(
        { transaction_ids: [row.transaction_id] },
        {
          onSuccess: (result) => {
            const why = row.reason === null ? '' : ` (${row.reason === 'guess' ? 'a guess' : 'unusual amount'})`
            offerUndo(`Approved ${row.payee}${why}`, result.undo_token, result.approved_ids)
          },
          onError: () => markApproved([row.transaction_id], false),
        },
      )
    },
    [approve, neighbour, offerUndo],
  )

  const fileRow = useCallback(
    (row: ReviewRow, categoryId: string) => {
      setPickingId(null)
      // Filing finishes a row, so focus goes to the next one that needs a look.
      const after = rows.slice(rows.findIndex((one) => one.transaction_id === row.transaction_id) + 1)
      setSelectedId((after.find((one) => !looksRight(one)) ?? neighbour(row.transaction_id))?.transaction_id ?? null)
      markApproved([row.transaction_id], true)
      file.mutate(
        { transaction_id: row.transaction_id, category_id: categoryId },
        {
          onSuccess: (result) => offerUndo(`Filed ${row.payee}`, result.undo_token, [row.transaction_id]),
          onError: () => markApproved([row.transaction_id], false),
        },
      )
    },
    [file, neighbour, offerUndo, rows],
  )

  const approveDay = useCallback(
    (day: ReviewDay) => {
      const sure = day.rows.filter((row) => looksRight(row) && !approvedIds.has(row.transaction_id))
      if (sure.length === 0) return
      const ids = sure.map((row) => row.transaction_id)
      markApproved(ids, true)
      approve.mutate(
        { transaction_ids: ids, only_if_looks_right: true },
        {
          onSuccess: (result) => {
            const skipped = result.skipped.map((row) => row.transaction_id)
            if (skipped.length > 0) markApproved(skipped, false)
            offerUndo(
              `Approved ${plural(result.approved_ids.length, 'row')} from ${dayLabel(day.date)}`,
              result.undo_token,
              result.approved_ids,
            )
          },
          onError: () => markApproved(ids, false),
        },
      )
    },
    [approve, approvedIds, offerUndo],
  )

  const handlers: RowHandlers = {
    selectedId,
    approvedIds,
    pickingId,
    categories: categories.data ?? [],
    frequentCategoryIds: [],
    onSelect: (row) => setSelectedId(row.transaction_id),
    onOpen: (row) => {
      setSelectedId(row.transaction_id)
      setOpenRow(row)
    },
    onApprove: approveRow,
    onPick: (row) => setPickingId(row?.transaction_id ?? null),
    onFile: fileRow,
    onApproveDay: approveDay,
  }

  useReviewKeys({
    rows,
    selectedId,
    busy: pickingId !== null || openRow !== null,
    select: setSelectedId,
    approve: approveRow,
    pick: (row) => setPickingId(row.transaction_id),
    open: (row) => setOpenRow(row),
    approveDay: (row) => {
      const day = allDays.find((one) => one.rows.some((each) => each.transaction_id === row.transaction_id))
      if (day) approveDay(day)
    },
  })

  const loading = summary.isPending || items.isPending || days.isPending
  const nothing = !loading && (items.data?.length ?? 0) === 0 && rows.length === 0

  return (
    <div className="page review">
      <PageHeader
        title={summary.data ? `${summary.data.badge} need you` : 'Review'}
        subtitle={summary.data ? badgeSentence(summary.data) : undefined}
      />

      {loading ? <SkeletonRows rows={6} /> : null}

      {nothing ? (
        <EmptyState
          icon={<CircleCheck size={20} />}
          title="Nothing to review"
          body="Every new transaction has been approved. New ones arrive with the next sync."
        />
      ) : null}

      <ItemBand items={items.data ?? []} />

      {allDays.map((day) => (
        <DaySection key={day.date} day={day} handlers={handlers} />
      ))}

      {days.hasNextPage ? (
        <Button
          variant="secondary"
          className="review__more"
          disabled={days.isFetchingNextPage}
          onClick={() => void days.fetchNextPage()}
        >
          Show earlier days
        </Button>
      ) : null}

      <p className="review__keys" aria-hidden="true">
        <kbd>j</kbd> <kbd>k</kbd> move · <kbd>x</kbd> approve · <kbd>c</kbd> category · <kbd>⇧</kbd>
        <kbd>x</kbd> approve day · <kbd>↵</kbd> open
      </p>

      <RowSheet
        row={openRow}
        onClose={() => setOpenRow(null)}
        onApprove={(row) => {
          setOpenRow(null)
          approveRow(row)
        }}
      />
    </div>
  )
}

interface ReviewKeys {
  rows: readonly ReviewRow[]
  selectedId: string | null
  /** A picker or the sheet is open, and owns the keyboard. */
  busy: boolean
  select: (id: string) => void
  approve: (row: ReviewRow) => void
  pick: (row: ReviewRow) => void
  open: (row: ReviewRow) => void
  approveDay: (row: ReviewRow) => void
}

/** `j`/`k` move, `x` approves, `c` picks a category, `X` approves the day, Enter opens. */
function useReviewKeys({ rows, selectedId, busy, select, approve, pick, open, approveDay }: ReviewKeys) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (busy || event.metaKey || event.ctrlKey || event.altKey) return
      const target = event.target instanceof HTMLElement ? event.target : null
      if (target && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))) return
      if (rows.length === 0) return

      const index = rows.findIndex((row) => row.transaction_id === selectedId)
      const current = index >= 0 ? rows[index] : null
      const move = (to: number) => {
        const row = rows[Math.max(0, Math.min(rows.length - 1, to))]
        select(row.transaction_id)
        document.querySelector<HTMLElement>(`[data-row-id="${row.transaction_id}"]`)?.focus()
      }

      switch (event.key) {
        case 'j':
          move(index + 1)
          break
        case 'k':
          move(index < 0 ? 0 : index - 1)
          break
        case 'x':
          if (current) approve(current)
          break
        case 'X':
          if (current) approveDay(current)
          break
        case 'c':
          if (current) pick(current)
          break
        case 'Enter':
          if (current) open(current)
          break
        default:
          return
      }
      event.preventDefault()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [rows, selectedId, busy, select, approve, pick, open, approveDay])
}
