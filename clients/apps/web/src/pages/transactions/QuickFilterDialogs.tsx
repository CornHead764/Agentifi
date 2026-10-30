import { ChevronDown, ChevronUp, Pencil, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'

import { DateRangeControl } from '@/components/transactions/DateRangeControl'
import { FilterFacets } from '@/components/transactions/FilterFacets'
import { REGISTER_FACETS } from '@/components/transactions/facets'
import {
  Button,
  Checkbox,
  ConfirmDialog,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  IconButton,
  Input,
  List,
  ListRow,
  useConfirm,
} from '@/components/ui'
import {
  useDeleteQuickFilter,
  useMoveQuickFilter,
  useUpdateQuickFilter,
} from '@/lib/clients/quickFilters'
import { labelForSelection, type DateSelection } from '@/lib/dateRanges'
import type { FilterDraft, FilterUniverse } from '@/lib/transactions/filter'
import {
  quickFilterFromSaved,
  quickFilterWrite,
  reorderedPositions,
  type QuickFilter,
} from '@/lib/transactions/quickFilters'
import type { AccountWithBalances, FilterRead, FilterWrite } from '@/lib/transactions/types'

/** What the facet editor needs to offer every choice the register's Filter popover does. */
export interface QuickFilterChoices {
  universe: FilterUniverse
  accounts: AccountWithBalances[]
  payees: string[]
}

interface QuickFilterDialogProps extends QuickFilterChoices {
  title: string
  submitLabel: string
  initial: Pick<QuickFilter, 'name' | 'panel' | 'search' | 'dates'>
  /** The window offered when the filter carries none of its own. */
  window: DateSelection
  pending: boolean
  onSubmit: (body: FilterWrite) => Promise<unknown>
  onClose: () => void
}

/**
 * One quick filter's name and what it asks for, saved new or edited: the
 * facets are `FilterFacets`, the one editor of the one `Filter`.
 */
export function QuickFilterDialog({
  title,
  submitLabel,
  initial,
  window,
  universe,
  accounts,
  payees,
  pending,
  onSubmit,
  onClose,
}: QuickFilterDialogProps) {
  const [name, setName] = useState(initial.name)
  const [panel, setPanel] = useState<FilterDraft>(initial.panel)
  const [search, setSearch] = useState(initial.search)
  const [keepDates, setKeepDates] = useState(initial.dates !== null)
  const [dates, setDates] = useState<DateSelection>(initial.dates ?? window)

  const submit = () => {
    if (pending || name.trim() === '') return
    const body = quickFilterWrite({ name, panel, search, dates: keepDates ? dates : null }, universe)
    // A refusal is already a toast; the dialog stays open holding the edit.
    void onSubmit(body).then(onClose, () => undefined)
  }

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        title={title}
        onSubmit={submit}
        footer={
          <DialogActions>
            <Button type="submit" variant="primary" disabled={pending || name.trim() === ''}>
              {pending ? 'Saving…' : submitLabel}
            </Button>
          </DialogActions>
        }
      >
        <div className="settings__form">
          <Field label="Name">
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Coffee runs"
              autoComplete="off"
              autoFocus
            />
          </Field>
          <Field label="Search" hint="Optional. Read as the register's search box reads it.">
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              autoComplete="off"
            />
          </Field>
          <Field label="Dates" as="group" hint="Without a date range, choosing it keeps the register's own.">
            <Checkbox
              label="Set a date range"
              checked={keepDates}
              onCheckedChange={(checked) => setKeepDates(checked === true)}
            />
            {keepDates ? <DateRangeControl value={dates} onChange={setDates} /> : null}
          </Field>
          <Field label="Filter" as="group">
            <div className="watchlist-form__facets">
              <FilterFacets
                draft={panel}
                categories={universe.categories}
                tags={universe.tags}
                accounts={accounts}
                payees={payees}
                facets={REGISTER_FACETS}
                compact
                onChange={setPanel}
              />
            </div>
          </Field>
        </div>
      </DialogContent>
    </Dialog>
  )
}

interface ManageProps extends QuickFilterChoices {
  rows: readonly FilterRead[]
  window: DateSelection
  today: Date
  onClose: () => void
}

/** The saved quick filters: edit, arrange and delete. */
export function ManageQuickFiltersDialog({
  rows,
  window,
  today,
  universe,
  accounts,
  payees,
  onClose,
}: ManageProps) {
  const [editing, setEditing] = useState<FilterRead | null>(null)
  const update = useUpdateQuickFilter()
  const move = useMoveQuickFilter()
  const remove = useConfirm(useDeleteQuickFilter(), { variables: (row: FilterRead) => row.id })
  const quick = useMemo(
    () => rows.map((row) => quickFilterFromSaved(row, universe, today)),
    [rows, universe, today],
  )
  const positions = rows.map((row) => ({ id: row.id, position: row.position ?? 0 }))
  const shift = (from: number, to: number) => {
    const moves = reorderedPositions(positions, from, to)
    if (moves.length > 0) move.mutate(moves)
  }

  return (
    <>
      <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
        <DialogContent title="Saved quick filters" footer={<DialogActions cancel="Close" />}>
          {rows.length === 0 ? (
            <p className="hint">No saved quick filters.</p>
          ) : (
            <List>
              {rows.map((row, index) => (
                <ListRow
                  key={row.id}
                  title={quick[index]!.name}
                  sub={quick[index]!.dates === null ? null : labelForSelection(quick[index]!.dates!)}
                  actions={
                    <>
                      <IconButton
                        label={`Move ${quick[index]!.name} up`}
                        variant="ghost"
                        size="sm"
                        disabled={index === 0 || move.isPending}
                        onClick={() => shift(index, index - 1)}
                      >
                        <ChevronUp size={13} />
                      </IconButton>
                      <IconButton
                        label={`Move ${quick[index]!.name} down`}
                        variant="ghost"
                        size="sm"
                        disabled={index === rows.length - 1 || move.isPending}
                        onClick={() => shift(index, index + 1)}
                      >
                        <ChevronDown size={13} />
                      </IconButton>
                      <IconButton
                        label={`Edit ${quick[index]!.name}`}
                        variant="ghost"
                        size="sm"
                        onClick={() => setEditing(row)}
                      >
                        <Pencil size={13} />
                      </IconButton>
                      <IconButton
                        label={`Delete ${quick[index]!.name}`}
                        variant="ghost"
                        size="sm"
                        onClick={() => remove.ask(row)}
                      >
                        <Trash2 size={13} />
                      </IconButton>
                    </>
                  }
                />
              ))}
            </List>
          )}
        </DialogContent>
      </Dialog>

      {editing === null ? null : (
        <QuickFilterDialog
          title={`Edit ${editing.name ?? 'quick filter'}`}
          submitLabel="Save"
          initial={quickFilterFromSaved(editing, universe, today)}
          window={window}
          universe={universe}
          accounts={accounts}
          payees={payees}
          pending={update.isPending}
          onSubmit={(body) => update.mutateAsync({ id: editing.id, body })}
          onClose={() => setEditing(null)}
        />
      )}

      <ConfirmDialog
        {...remove.dialog}
        title={`Delete ${remove.target?.name ?? 'this quick filter'}?`}
        description="Only the quick filter goes; no transaction changes."
        confirmLabel="Delete quick filter"
      />
    </>
  )
}
