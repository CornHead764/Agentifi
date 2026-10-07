import { useQuery } from '@tanstack/react-query'
import { useMemo, useState, type ReactNode } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import { FilterFacets } from '@/components/transactions/FilterFacets'
import type { FacetId } from '@/components/transactions/facets'
import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  EmptyState,
  Field,
  Input,
  MoneyInput,
  OptionSelect,
  SkeletonRows,
  Tabs,
  TabsList,
  TabsTrigger,
} from '@/components/ui'
import { useSavedReports } from '@/lib/clients/reports'
import { amountFieldError } from '@/lib/money'
import type { FilterUniverse } from '@/lib/transactions/filter'
import { useAccounts, useCategories, usePayees, useTags } from '@/lib/transactions/queries'
import {
  WATCHLISTS_KEY,
  WATCHLIST_PERIODS,
  blankWatchlistForm,
  buildWatchlistBody,
  watchlistFormFrom,
  watchlistsApi,
  type WatchlistBody,
  type WatchlistForm,
  type WatchlistSummary,
} from '@/lib/watchlists'

/** Every facet: a card can be about anything the register can narrow to. */
const WATCHLIST_FACETS: readonly FacetId[] = [
  'categories',
  'payees',
  'tags',
  'accounts',
  'flags',
  'amount',
  'advanced',
]

interface DialogProps {
  /** The watchlist being edited; absent for a new one. */
  editing?: WatchlistSummary
  pending: boolean
  onSubmit: (body: WatchlistBody) => Promise<unknown>
  onClose: () => void
}

/**
 * New watchlist, or an existing one to edit. Which transactions count is asked
 * by `FilterFacets`, the one editor of the one `Filter`.
 *
 * It can instead point at a saved report, whose filter the server accepts by
 * id. The row is then shared: editing the report changes what the card
 * counts, which the sheet says. Editing the card's facets never edits the
 * report; the server gives the card a filter of its own.
 */
export function WatchlistDialog(props: DialogProps) {
  const categories = useCategories()
  const tags = useTags()
  const filterId = props.editing?.filter_id
  const stored = useQuery({
    queryKey: [...WATCHLISTS_KEY, 'filter', filterId],
    queryFn: ({ signal }) => watchlistsApi.filter(filterId ?? '', signal),
    enabled: filterId !== undefined,
    // Reopening after an edit must read the filter as it is now.
    gcTime: 0,
  })
  const universe = useMemo<FilterUniverse>(
    () => ({ categories: categories.data ?? [], tags: tags.data ?? [] }),
    [categories.data, tags.data],
  )

  const title = props.editing ? `Edit ${props.editing.name}` : 'New watchlist'
  const loading = categories.isPending || tags.isPending

  if (props.editing === undefined) {
    return loading ? (
      <LoadingDialog title={title} onClose={props.onClose} />
    ) : (
      <WatchlistFormDialog
        {...props}
        title={title}
        initial={blankWatchlistForm()}
        universe={universe}
      />
    )
  }
  const editing = props.editing
  if (loading || stored.isPending || stored.error) {
    return (
      <LoadingDialog title={title} onClose={props.onClose}>
        <QueryBoundary query={stored}>{() => null}</QueryBoundary>
      </LoadingDialog>
    )
  }
  return (
    <WatchlistFormDialog
      {...props}
      title={title}
      initial={watchlistFormFrom(editing, stored.data, universe)}
      universe={universe}
    />
  )
}

function LoadingDialog({
  title,
  onClose,
  children,
}: {
  title: string
  onClose: () => void
  children?: ReactNode
}) {
  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        title={title}
        footer={<DialogActions />}
      >
        {children ?? <SkeletonRows rows={4} />}
      </DialogContent>
    </Dialog>
  )
}

function WatchlistFormDialog({
  editing,
  title,
  initial,
  universe,
  pending,
  onSubmit,
  onClose,
}: DialogProps & { title: string; initial: WatchlistForm; universe: FilterUniverse }) {
  const [form, setForm] = useState(initial)
  const set = (patch: Partial<WatchlistForm>) => setForm((current) => ({ ...current, ...patch }))
  const accounts = useAccounts()
  const payees = usePayees()

  const body = buildWatchlistBody(form, universe)
  // Shown once a save is tried: a half-typed "1,0" is not a mistake yet.
  const [tried, setTried] = useState(false)
  const targetError = amountFieldError(form.target)

  const submit = () => {
    setTried(true)
    if (body === null) return
    // A refusal is already a toast, and the dialog stays open holding what was
    // typed so it can be corrected rather than retyped.
    void onSubmit(body).then(onClose, () => undefined)
  }

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        title={title}
        description="This month, year to date and a trailing average for one selection."
        onSubmit={submit}
        footer={
          <DialogActions>
            <Button type="submit" variant="primary" disabled={pending || (body === null && targetError === null)}>
              {editing ? 'Save' : 'Create watchlist'}
            </Button>
          </DialogActions>
        }
      >
        <div className="settings__form">
          <Field label="Name">
            <Input
              value={form.name}
              onChange={(event) => set({ name: event.target.value })}
              placeholder="Groceries"
              autoComplete="off"
            />
          </Field>

          <Field label="Emoji" hint="Optional. Shown on the card in place of the default glyph.">
            <Input
              value={form.emoji}
              onChange={(event) => set({ emoji: event.target.value })}
              placeholder="🛒"
              autoComplete="off"
            />
          </Field>

          <Field
            label="Target"
            hint="Optional. The card flags the month once spending crosses it."
            error={tried ? targetError : null}
          >
            <MoneyInput
              value={form.target}
              onChange={(event) => set({ target: event.target.value })}
              placeholder="400.00"
            />
          </Field>

          <Field
            label="Period"
            hint="How often the target starts over. The card's figures stay monthly."
          >
            <OptionSelect
              value={form.period}
              onValueChange={(period) => set({ period })}
              aria-label="Period"
              options={WATCHLIST_PERIODS}
            />
          </Field>

          <Field
            label="Watching"
            as="group"
            hint="Every transaction matching all of these counts toward the card."
          >
            <Tabs
              value={form.source}
              onValueChange={(value) => set({ source: value === 'report' ? 'report' : 'filter' })}
            >
              <TabsList>
                <TabsTrigger value="filter">Transactions</TabsTrigger>
                <TabsTrigger value="report">Saved report</TabsTrigger>
              </TabsList>
            </Tabs>

            {form.source === 'report' ? (
              <ReportSelect
                value={form.reportFilterId}
                onChange={(reportFilterId) => set({ reportFilterId })}
              />
            ) : (
              <div className="watchlist-form__facets">
                <FilterFacets
                  draft={form.filter}
                  categories={universe.categories}
                  tags={universe.tags}
                  accounts={accounts.data ?? []}
                  payees={payees.data ?? []}
                  facets={WATCHLIST_FACETS}
                  compact
                  onChange={(filter) => set({ filter })}
                />
              </div>
            )}
          </Field>
        </div>
      </DialogContent>
    </Dialog>
  )
}

/** The saved reports, as filters to watch. One at a time: two reports have no rule for combining. */
function ReportSelect({
  value,
  onChange,
}: {
  value: string
  onChange: (filterId: string) => void
}) {
  const reports = useSavedReports()
  return (
    <QueryBoundary
      query={reports}
      rows={2}
      empty={(rows) =>
        rows.length === 0 ? <EmptyState compact title="No saved reports yet." /> : undefined
      }
    >
      {(rows) => (
        <>
          <OptionSelect
            value={value}
            onValueChange={onChange}
            aria-label="Saved report"
            placeholder="Choose a report"
            options={rows.map((report) => ({ value: report.filter.id, label: report.name }))}
          />
          <p className="field__hint">
            Uses the report's filter. Editing the report changes the card.
          </p>
        </>
      )}
    </QueryBoundary>
  )
}
