import { Pause, Pencil, Play, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'

import { Money } from '@/components/Money'
import { SeriesEditor } from '@/components/SeriesEditor'
import {
  Badge,
  ConfirmDialog,
  OverflowMenu,
  RowActions,
  Table,
  TableEmptyRow,
  Td,
  Th,
  useConfirm,
} from '@/components/ui'
import { accountTypeLabel } from '@/lib/accountTypes'
import { categoryName as categoryNameFor } from '@/lib/categoryNames'
import {
  SERIES_KIND_LABELS,
  useDeleteSeries,
  useSetSeriesActive,
  type Series,
} from '@/lib/clients/upcoming'
import { EM_DASH } from '@/lib/format'
import { useLinkedItem, useScrollToLinked } from '@/lib/linkedItem'
import { dueState, nextDueText } from '@/pages/settings/recurring/rows'
import { shortLabel } from '@/lib/recurrence'
import { useAccounts, useCategories, useTags } from '@/lib/transactions/queries'
import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { seriesSubject } from '@/lib/assistant/subjects'

/** The Recurring tab's list, the only list of recurring items, so every row has edit, pause and delete. */
export function SeriesTable({ rows }: { rows: readonly Series[] }) {
  const categories = useCategories()
  const accounts = useAccounts()
  const tags = useTags()

  const [editing, setEditing] = useState<Series | null>(null)

  // `?series=<id>`: an accepted assistant card leads here, to the row it made.
  const link = useLinkedItem('series')
  const linked = link.linked
  useScrollToLinked(linked, rows.length > 0)
  const shown =
    editing ?? (link.opening === null ? null : (rows.find((one) => one.id === link.opening) ?? null))

  const setActive = useSetSeriesActive()
  const remove = useConfirm(useDeleteSeries(), {
    variables: (series: Series) => series.id,
  })

  const categoryName = useMemo(
    () => (id: string | null) => categoryNameFor(categories.data ?? [], id),
    [categories.data],
  )

  const account = useMemo(() => {
    const byId = new Map((accounts.data ?? []).map((one) => [one.id, one]))
    return (id: string) => byId.get(id)
  }, [accounts.data])

  if (rows.length === 0) {
    return (
      <Table lines={2}>
        <tbody>
          <TableEmptyRow colSpan={6}>Nothing recurring in this group yet.</TableEmptyRow>
        </tbody>
      </Table>
    )
  }

  return (
    <>
      <Table lines={2} stack>
        <thead>
          <tr>
            <Th>Name</Th>
            <Th>Account</Th>
            <Th>Category</Th>
            <Th numeric>Per occurrence</Th>
            <Th numeric>Yearly</Th>
            <Th />
          </tr>
        </thead>
        <tbody>
          {rows.map((series) => {
            const seriesAccount = account(series.account_id)
            return (
              <tr key={series.id} data-linked={series.id === linked ? 'true' : undefined}>
                <Td label="" className="series__lead">
                  <span className="series__name">
                    <span className="series__label">{series.label}</span>
                    {series.is_active ? null : <Badge>Paused</Badge>}
                  </span>
                  <span className={dueState(series) === 'overdue' ? 'cell__sub series__overdue' : 'cell__sub'}>
                    {dueState(series) === 'upcoming' ? 'Next: ' : ''}
                    {nextDueText(series)}
                  </span>
                </Td>
                <Td className="stack-inline nowrap">
                  <span>{seriesAccount?.name ?? 'Unknown account'}</span>
                  <span className="cell__sub">
                    {seriesAccount ? accountTypeLabel(seriesAccount.type) : EM_DASH}
                  </span>
                </Td>
                <Td className="stack-inline nowrap">
                  <span>{categoryName(series.category_id)}</span>
                  <span className="cell__sub">{SERIES_KIND_LABELS[series.kind]}</span>
                </Td>
                <Td numeric className="stack-inline nowrap">
                  <Money value={series.amount} showPlus={series.amount > 0} />
                  <span className="cell__sub">{shortLabel(series.recurrence)}</span>
                </Td>
                <Td numeric className="stack-inline nowrap">
                  {/* Over a real calendar year: biweekly is 26 or 27. A one-time
                      payment has no year, and "$0.00, 0× a year" would read as a
                      figure. */}
                  {series.occurrences_per_year === 0 ? (
                    <span className="muted">One-time</span>
                  ) : (
                    <>
                      <Money
                        value={series.annualized_amount}
                        showPlus={series.annualized_amount > 0}
                      />
                      <span className="cell__sub">{series.occurrences_per_year}× a year</span>
                    </>
                  )}
                </Td>
                <Td numeric label="" className="stack-corner">
                  <RowActions>
                    <OverflowMenu
                      label={`Actions for ${series.label}`}
                      actions={[
                        { label: 'Edit', icon: <Pencil size={14} />, onSelect: () => setEditing(series) },
                        {
                          label: series.is_active ? 'Pause' : 'Resume',
                          icon: series.is_active ? <Pause size={14} /> : <Play size={14} />,
                          onSelect: () =>
                            setActive.mutate({ id: series.id, active: !series.is_active }),
                        },
                        <AskMenuItem subject={() => seriesSubject(series)} />,
                        {
                          label: 'Delete',
                          icon: <Trash2 size={14} />,
                          danger: true,
                          onSelect: () => remove.ask(series),
                        },
                      ]}
                    />
                  </RowActions>
                </Td>
              </tr>
            )
          })}
        </tbody>
      </Table>

      <SeriesEditor
        open={shown !== null}
        onOpenChange={(open) => {
          if (open) return
          setEditing(null)
          link.settle()
        }}
        series={shown}
        accounts={accounts.data ?? []}
        categories={categories.data ?? []}
        tags={tags.data ?? []}
      />

      {/* A soft delete: the charges this series matched keep their link to it, so
          nothing in the register moves and no spending history changes. */}
      <ConfirmDialog
        {...remove.dialog}
        title={`Delete ${remove.target?.label ?? 'this recurring item'}?`}
        description="Stops projecting reminders. Matched transactions keep their link; no history changes."
        confirmLabel="Delete"
      />
    </>
  )
}
