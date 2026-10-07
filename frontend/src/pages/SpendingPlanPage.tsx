import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { clsx } from 'clsx'
import { ChevronLeft, ChevronRight, Lock, Pencil, Plus, Undo2, Zap } from 'lucide-react'
import { useState } from 'react'
import { Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom'

import { Money } from '@/components/Money'
import { BucketOverrideDialog } from '@/components/plan/BucketOverrideDialog'
import { BucketRail } from '@/components/plan/BucketRail'
import {
  AddPlannedExpenseDialog,
  AddToPlannedSpendDialog,
  EnvelopeCategoriesDialog,
  EnvelopeEditDialog,
  type EnvelopeEditMode,
} from '@/components/plan/EnvelopeDialogs'
import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import {
  EnvelopeControls,
  EnvelopePanel,
  type EnvelopeSort,
  type EnvelopeView,
} from '@/components/plan/EnvelopePanel'
import { EnvelopeTransactionsDialog } from '@/components/plan/EnvelopeTransactionsDialog'
import { OtherSpendPanel } from '@/components/plan/OtherSpendPanel'
import { PlanEntryDialog } from '@/components/plan/PlanEntryDialog'
import { PlanEntryPanel } from '@/components/plan/PlanEntryPanel'
import { InfoTip } from '@/components/InfoTip'
import { LoadFailure } from '@/components/QueryBoundary'
import { SeriesEditor } from '@/components/SeriesEditor'
import {
  CreateFromTransaction,
  type CreateTarget,
} from '@/components/transactions/CreateFromTransaction'
import { LinkSeriesDialog } from '@/components/transactions/LinkSeriesDialog'
import {
  Button,
  Callout,
  Card,
  IconButton,
  OverflowMenu,
  PageHeader,
  RowActions,
  SkeletonRows,
  Spinner,
  Tooltip,
  useToast,
} from '@/components/ui'
import { describeBulk, runInOrder } from '@/components/plan/bulkEnvelope'
import type { Series } from '@/lib/clients/upcoming'
import {
  useAccounts,
  useCategories,
  useLinkSeries,
  useTags,
  useUnlinkSeries,
} from '@/lib/transactions/queries'
import type { Transaction } from '@/lib/transactions/types'
import { bucketSubject } from '@/lib/assistant/subjects'
import {
  BUCKET_LABELS,
  BUCKET_PATHS,
  bucketFromPath,
  monthFromParam,
  shiftMonth,
  effectiveAmount,
  envelopeSeedCategories,
  isOverridden,
  spendingPlanApi,
  type BucketKey,
  type Envelope,
  type OtherSpendSlice,
  type PlanEntry,
  type ProjectionType,
  type SpendingPlanMonth,
  spendingPlanKeys,
} from '@/lib/spendingPlan'
import { formatCount, formatDate, formatMonthKey, monthKey, plural } from '@/lib/format'
import { invalidateOccurrenceWrites } from '@/lib/clients/upcoming'

/**
 * The spending plan: five buckets, a carried balance, one number. Selecting a
 * bucket shows the rows that made it, the rows dropped this month, and any
 * override: a materialized month exists so its figures can be explained.
 */
export function SpendingPlanPage() {
  // The bucket and the month both live in the URL
  // (/spending-plan/bills?date=2026-08-01), so back, forward and a pasted link
  // land on the same panel.
  const params = useParams()
  const [search] = useSearchParams()
  const navigate = useNavigate()
  const selected = bucketFromPath(params.bucket)
  const month = monthFromParam(search.get('date')) ?? monthKey()
  const [addingExpense, setAddingExpense] = useState(false)
  const [converting, setConverting] = useState<OtherSpendSlice | null>(null)
  const [editing, setEditing] = useState<{
    envelope: Envelope
    mode: EnvelopeEditMode
  } | null>(null)
  const [envelopeView, setEnvelopeView] = useState<EnvelopeView>('list')
  const [envelopeSort, setEnvelopeSort] = useState<EnvelopeSort>('name')
  const [recategorizing, setRecategorizing] = useState<Envelope | null>(null)
  const [overriding, setOverriding] = useState(false)
  // The row a person opened, and the editors it can hand off to: the series
  // behind it, a series to link its charge to, or a new series seeded from it.
  const [opening, setOpening] = useState<PlanEntry | null>(null)
  const [envelopeRows, setEnvelopeRows] = useState<Envelope | null>(null)
  const [editingSeries, setEditingSeries] = useState<Series | null>(null)
  const [linking, setLinking] = useState<Transaction | null>(null)
  const [creating, setCreating] = useState<CreateTarget>(null)

  const toast = useToast()
  const queryClient = useQueryClient()
  const key = spendingPlanKeys.month(month)
  const refreshPlan = () => invalidateOccurrenceWrites(queryClient)
  const ledger = {
    accounts: useAccounts(),
    categories: useCategories(),
    tags: useTags(),
  }
  const linkToSeries = useLinkSeries()
  const unlinkFromSeries = useUnlinkSeries()

  const planPath = (bucket: BucketKey, toMonth: string) => {
    const segment = bucket === 'rollover' ? '' : BUCKET_PATHS[bucket]
    const path = segment === '' ? '/spending-plan' : `/spending-plan/${segment}`
    return `${path}?date=${toMonth}-01`
  }

  const plan = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => spendingPlanApi.month(month, signal),
    placeholderData: (previous) => previous,
  })

  // Fetched separately from the month, so a category with no spend can still
  // be given an envelope.
  const categories = useQuery({
    queryKey: spendingPlanKeys.expenseCategories,
    queryFn: ({ signal }) => spendingPlanApi.categories(signal),
  })

  const mutate = useMutation({
    mutationFn: (run: () => Promise<SpendingPlanMonth | unknown>) => run(),
    onSuccess: refreshPlan,
    meta: { failure: 'That change did not save' },
  })

  if (selected === null) {
    return <Navigate to="/spending-plan" replace />
  }

  if (plan.isPending) {
    return (
      <div className="page">
        <SkeletonRows rows={6} />
      </div>
    )
  }

  if (plan.isError || !plan.data) {
    return (
      <div className="page">
        <LoadFailure query={plan} title="Could not load the spending plan" />
      </div>
    )
  }

  const data = plan.data
  // Stepping months keeps the last month drawn until the next one arrives;
  // nothing below the header may write to it meanwhile.
  const stale = plan.isPlaceholderData
  const frozen = data.is_closed_out
  const bucket = data.buckets[selected]
  const overridden = isOverridden(bucket)
  // Rollover is the one bucket the server refuses an override on, and it has no
  // route of its own — every bucket that can be selected here can take one.

  return (
    <div className="page">
      <PageHeader
        title={<MonthLabel month={month} />}
        actions={
          <Button
            variant="primary"
            size="sm"
            disabled={frozen || stale}
            onClick={() => setAddingExpense(true)}
          >
            <Plus size={13} /> New expense
          </Button>
        }
      >
        <div className="plan__nav">
          <IconButton
            label="Previous month"
            variant="ghost"
            size="sm"
            onClick={() => navigate(planPath(selected, shiftMonth(month, -1)))}
          >
            <ChevronLeft size={14} />
          </IconButton>
          <IconButton
            label="Next month"
            variant="ghost"
            size="sm"
            onClick={() => navigate(planPath(selected, shiftMonth(month, 1)))}
          >
            <ChevronRight size={14} />
          </IconButton>
          {month === monthKey() ? null : (
            <Button variant="ghost" size="sm" onClick={() => navigate(planPath(selected, monthKey()))}>
              This month
            </Button>
          )}
          {stale ? <Spinner label="Loading the month" /> : null}
        </div>
      </PageHeader>

      {frozen ? (
        <Callout icon={<Lock size={14} />}>
          <p>
            Closed out{data.closed_out_at ? ` ${formatDate(data.closed_out_at)}` : ''}, figures
            frozen{' '}
            <InfoTip>Editing a transaction dated this month changes reports, not this plan.</InfoTip>
          </p>
        </Callout>
      ) : null}

      <div className={clsx('plan', stale && 'stale')} aria-busy={stale}>
        <BucketRail
          month={data}
          selected={selected}
          onSelect={(bucket) => navigate(planPath(bucket, month))}
        />

        <Card
          title={
            <>
              {BUCKET_LABELS[selected]}{' '}
              <Money value={effectiveAmount(bucket)} showPlus={selected === 'income'} />
            </>
          }
          subtitle={
            <Tooltip
              label={`${plural(bucket.contributing.length, 'row')} counted, ${formatCount(bucket.excluded.length)} excluded this month`}
              side="bottom"
            >
              <span>
                {formatCount(bucket.contributing.length)} counted ·{' '}
                {formatCount(bucket.excluded.length)} excluded
              </span>
            </Tooltip>
          }
          actions={
            <>
              {selected === 'planned_spend' ? (
                <EnvelopeControls
                  view={envelopeView}
                  sort={envelopeSort}
                  onView={setEnvelopeView}
                  onSort={setEnvelopeSort}
                />
              ) : null}
              <RowActions>
                <OverflowMenu
                  label={`Actions for ${BUCKET_LABELS[selected]}`}
                  actions={[
                    <AskMenuItem
                      subject={() =>
                        bucketSubject({
                          name: BUCKET_LABELS[selected],
                          month: data.month,
                          amount: bucket.effective_amount,
                        })
                      }
                    />,
                  ]}
                  sections={[
                    {
                      entries:
                        selected === 'planned_spend'
                          ? [
                              {
                                label: 'Release all unspent funds',
                                icon: <Undo2 size={14} />,
                                disabled: frozen,
                                onSelect: () =>
                                  mutate.mutate(() =>
                                    spendingPlanApi.releaseAllRollover(data.month),
                                  ),
                              },
                              {
                                label: 'Turn on auto-release',
                                icon: <Zap size={14} />,
                                disabled:
                                  frozen ||
                                  data.envelopes.every((envelope) => envelope.auto_release_rollover),
                                onSelect: () =>
                                  // One envelope at a time: each PATCH recalculates the
                                  // whole month, and racing those recalculations makes them
                                  // disagree about the chain. A refusal does not stop the
                                  // run; the month is refetched either way.
                                  mutate.mutate(async () => {
                                    const result = await runInOrder(
                                      data.envelopes.filter((one) => !one.auto_release_rollover),
                                      (envelope) =>
                                        spendingPlanApi.updateEnvelope(data.month, envelope.id, {
                                          auto_release_rollover: true,
                                        }),
                                    )
                                    toast.show(
                                      describeBulk(
                                        result,
                                        (envelope) => envelope.name,
                                        'Auto-release on',
                                      ),
                                    )
                                  }),
                              },
                            ]
                          : [],
                    },
                    {
                      entries: [
                        {
                          label: "Override this month's amount…",
                          icon: <Pencil size={14} />,
                          disabled: frozen,
                          onSelect: () => setOverriding(true),
                        },
                        overridden && {
                          label: 'Reset override',
                          disabled: frozen,
                          onSelect: () =>
                            mutate.mutate(() =>
                              spendingPlanApi.overrideBucket(data.month, selected, {
                                overwritten_amount: null,
                              }),
                            ),
                        },
                      ],
                    },
                  ]}
                />
              </RowActions>
            </>
          }
        >
          {selected === 'planned_spend' ? (
            <EnvelopePanel
              month={data.month}
              envelopes={data.envelopes}
              frozen={frozen}
              view={envelopeView}
              sort={envelopeSort}
              onReleaseRollover={(envelope) => setEditing({ envelope, mode: 'release' })}
              onChangeRollover={(envelope) => setEditing({ envelope, mode: 'rollover' })}
              onEditTarget={(envelope) => setEditing({ envelope, mode: 'target' })}
              onClearTarget={(envelope) =>
                mutate.mutate(() =>
                  spendingPlanApi.updateEnvelope(data.month, envelope.id, {
                    overwritten_target_amount: null,
                  }),
                )
              }
              onEditCategories={setRecategorizing}
              onShowTransactions={setEnvelopeRows}
            />
          ) : selected === 'other_spend' ? (
            <OtherSpendPanel
              month={data}
              frozen={frozen}
              onAddToPlanned={setConverting}
              onProjectionChange={(type: ProjectionType, windowMonths: number) =>
                mutate.mutate(() =>
                  spendingPlanApi.updateProjection(data.month, {
                    type,
                    window_months: windowMonths,
                  }),
                )
              }
              onBufferChange={(buffer) =>
                mutate.mutate(() =>
                  spendingPlanApi.updateProjection(data.month, {
                    type: data.projection.type,
                    buffer,
                  }),
                )
              }
              onOpen={setOpening}
            />
          ) : (
            <PlanEntryPanel
              bucket={bucket}
              frozen={frozen}
              emptyTitle={`No ${BUCKET_LABELS[selected].toLowerCase()} this month`}
              onExclude={(entry) =>
                mutate.mutate(() => spendingPlanApi.excludeEntry(data.month, selected, entry.id))
              }
              onInclude={(entry) =>
                mutate.mutate(() => spendingPlanApi.includeEntry(data.month, selected, entry.id))
              }
              onOpen={setOpening}
            />
          )}
        </Card>
      </div>

      <PlanEntryDialog
        entry={opening}
        month={data.month}
        isIncome={selected === 'income'}
        frozen={frozen}
        onOpenChange={(open) => (open ? undefined : setOpening(null))}
        onExclude={(entry) => {
          mutate.mutate(() => spendingPlanApi.excludeEntry(data.month, selected, entry.id))
          setOpening(null)
        }}
        onEditSeries={(series) => {
          setEditingSeries(series)
          setOpening(null)
        }}
        onLinkSeries={(txn) => {
          setLinking(txn)
          setOpening(null)
        }}
        onUnlinkSeries={(txn) => {
          unlinkFromSeries.mutate(txn.id, {
            onSuccess: () => {
              refreshPlan()
              toast.show({ title: 'Unlinked from its recurring item' })
            },
          })
          setOpening(null)
        }}
        onCreateSeries={(txn) => {
          setCreating({ kind: 'series', txn })
          setOpening(null)
        }}
      />

      <EnvelopeTransactionsDialog
        envelope={envelopeRows}
        month={data.month}
        onOpenChange={(open) => (open ? undefined : setEnvelopeRows(null))}
      />

      <SeriesEditor
        open={editingSeries !== null}
        onOpenChange={(open) => (open ? undefined : setEditingSeries(null))}
        series={editingSeries}
        accounts={ledger.accounts.data ?? []}
        categories={ledger.categories.data ?? []}
        tags={ledger.tags.data ?? []}
        onSaved={() => {
          refreshPlan()
          setEditingSeries(null)
        }}
      />

      <LinkSeriesDialog
        txn={linking}
        accounts={ledger.accounts.data ?? []}
        onClose={() => setLinking(null)}
        onLink={(row, series, dueOn) => {
          linkToSeries.mutate(
            { id: row.id, seriesId: series.id, dueOn },
            {
              onSuccess: (updated) => {
                refreshPlan()
                toast.show({
                  title: 'Linked to the recurring item',
                  description: `Filed under ${series.label}, due ${updated.series_due_on ?? 'this occurrence'}.`,
                })
              },
            },
          )
          setLinking(null)
        }}
        onCreateInstead={(row) => {
          setLinking(null)
          setCreating({ kind: 'series', txn: row })
        }}
      />

      <CreateFromTransaction
        target={creating}
        onClose={() => setCreating(null)}
        accounts={ledger.accounts.data ?? []}
        categories={ledger.categories.data ?? []}
        tags={ledger.tags.data ?? []}
        onCreated={() => {
          refreshPlan()
          toast.show({
            title: 'Recurring item created',
            description: 'The plan now expects it each month.',
          })
        }}
      />

      <AddToPlannedSpendDialog
        slice={converting}
        onOpenChange={(open) => (open ? undefined : setConverting(null))}
        onConfirm={(slice, targetAmount) => {
          mutate.mutate(() =>
            spendingPlanApi.createEnvelope(data.month, {
              name: slice.category_name,
              target_amount: targetAmount,
              category_ids: envelopeSeedCategories(slice, categories.data ?? []),
              recurring: true,
              rollover: false,
            }),
          )
          setConverting(null)
        }}
      />

      <AddPlannedExpenseDialog
        open={addingExpense}
        onOpenChange={setAddingExpense}
        month={data.month}
        categories={categories.data ?? []}
        onCreate={(body) => mutate.mutate(() => spendingPlanApi.createEnvelope(data.month, body))}
      />

      <BucketOverrideDialog
        bucket={overriding ? bucket : null}
        bucketKey={selected}
        onOpenChange={(open) => (open ? undefined : setOverriding(false))}
        onSave={(amount) => {
          mutate.mutate(() =>
            spendingPlanApi.overrideBucket(data.month, selected, { overwritten_amount: amount }),
          )
          setOverriding(false)
        }}
      />

      <EnvelopeEditDialog
        envelope={editing?.envelope ?? null}
        mode={editing?.mode ?? 'release'}
        onOpenChange={(open) => (open ? undefined : setEditing(null))}
        onRelease={(envelope) => {
          mutate.mutate(() => spendingPlanApi.releaseRollover(data.month, envelope.id))
          setEditing(null)
        }}
        onSetAmount={(envelope, mode, amount) => {
          mutate.mutate(() =>
            spendingPlanApi.updateEnvelope(
              data.month,
              envelope.id,
              mode === 'target'
                ? { overwritten_target_amount: amount }
                : { rollover_amount: amount },
            ),
          )
          setEditing(null)
        }}
        onAutoRelease={(envelope, on) =>
          mutate.mutate(() =>
            spendingPlanApi.updateEnvelope(data.month, envelope.id, {
              auto_release_rollover: on,
            }),
          )
        }
      />

      <EnvelopeCategoriesDialog
        envelope={recategorizing}
        month={data.month}
        categories={categories.data ?? []}
        onOpenChange={(open) => (open ? undefined : setRecategorizing(null))}
        onSave={(envelope, patch) => {
          mutate.mutate(() => spendingPlanApi.updateEnvelope(data.month, envelope.id, patch))
          setRecategorizing(null)
        }}
      />
    </div>
  )
}

const MONTHS = Array.from({ length: 12 }, (_, index) => String(index + 1).padStart(2, '0'))

/**
 * The month's name, as wide as the widest month of its year in this locale,
 * so the arrows after it hold still while it changes. Every name is laid in
 * one grid cell and only the month shown is visible.
 */
function MonthLabel({ month }: { month: string }) {
  const year = month.slice(0, 4)
  return (
    <span className="plan__month">
      <span>{formatMonthKey(month)}</span>
      {MONTHS.map((one) => (
        <span key={one} className="plan__month-sizer" aria-hidden="true">
          {formatMonthKey(`${year}-${one}`)}
        </span>
      ))}
    </span>
  )
}
