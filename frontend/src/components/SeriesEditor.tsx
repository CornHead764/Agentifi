/**
 * The series editor, create and edit in one dialog, opened by Bills & Income,
 * the register and the spending plan. Picking a transaction seeds the
 * *matching* text from its statement name, not its payee.
 *
 * The two names never merge: `name` writes `display_name`, and `description`
 * is what a charge is compared against. The name box is seeded from
 * `display_name` alone; seeding from `label` would save the bank's wording as
 * a name.
 *
 * Match Criteria is an amount band, separate from the wording match.
 */

import { Plus, Split as SplitIcon } from 'lucide-react'
import { useState } from 'react'

import { ConnectionDialog } from '@/pages/settings/bills/ConnectionDialog'
import { useProviderFollowUp } from '@/pages/settings/bills/providerFollowUp'

import {
  Button,
  Callout,
  DialogActions,
  DialogContent,
  EmptyState,
  Field,
  FormDialog,
  Input,
  MoneyInput,
  OptionSelect,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from '@/components/ui'
import { AccountSelect } from '@/components/AccountSelect'
import { connectionTitle } from '@/lib/billers'
import {
  useBillConnections,
  useBillSubaccounts,
  useLinkSeriesToBill,
  useSeriesBillLink,
  useUnlinkSeriesFromBill,
} from '@/lib/clients/bills'
import {
  SERIES_KIND_LABELS,
  draftFromEdits,
  editsFromSeries,
  editsFromSuggestion,
  patchFromEdits,
  seriesAmountErrors,
  splitParent,
  splitsBalance,
  startSplitting,
  useCreateSeries,
  useUpdateSeries,
  type MatchCriteria,
  type Series,
  type SeriesEdits,
  type SeriesKind,
  type SuggestedSeries,
} from '@/lib/clients/upcoming'
import { toIsoDate } from '@/lib/format'
import type { Category, Tag, Uuid } from '@/lib/transactions/types'
import { recurrenceFor } from '@/lib/recurrence'
import { describeApiError } from '@/lib/transactions/queries'

import { CategoryPicker, TagPicker } from './transactions/Pickers'
import { SplitEditor } from './transactions/SplitEditor'
import { SeriesSchedule, type SchedulePatch } from './SeriesSchedule'

const MATCH_LABELS: Record<MatchCriteria, string> = {
  auto: 'Auto Match',
  any: 'Any Amount',
  exact: 'Exact Amount',
  range: 'Limited Range',
}

const MATCH_HINTS: Record<MatchCriteria, string> = {
  auto: 'Widened from what this item has matched before.',
  any: 'Any amount on the right account and around the right date.',
  exact: 'Only a charge for exactly this amount.',
  range: 'Only a charge inside the band below.',
}

export interface SeedTransaction {
  statement_name: string
  payee: string
  account_id: string
  category_id: string | null
  amount: string
  date: string
}

export interface SeriesEditorProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The series being edited. Null creates one. */
  series?: Series | null
  /** Fixes the kind and hides the Type select, as the Refunds tab does. */
  fixedKind?: SeriesKind
  /** Set when a create was started from an existing transaction. */
  seed?: SeedTransaction | null
  /**
   * What the server read out of the history for that transaction, or out of a
   * billed account's bills. Takes precedence over `seed`, which can only
   * guess at monthly; null when the server had nothing to add.
   */
  suggestion?: SuggestedSeries | null
  /** A billed account a new series is linked to on save, preselected in Bill Connect. */
  billLink?: { connectionId: string; subaccountId: string } | null
  /** Said above the form: how a suggestion was read, and what to check. */
  notice?: string | null
  accounts: readonly { id: string; name: string }[]
  /** The template's vocabulary, so the editor can show the category and tags it holds. */
  categories: readonly Category[]
  tags: readonly Tag[]
  onSaved?: (series: Series) => void
}

export function SeriesEditor({
  open,
  onOpenChange,
  series = null,
  fixedKind,
  seed = null,
  suggestion = null,
  billLink = null,
  notice = null,
  accounts,
  categories,
  tags,
  onSaved,
}: SeriesEditorProps) {
  return (
    <FormDialog open={open} onOpenChange={onOpenChange}>
      <EditorBody
        series={series}
        fixedKind={fixedKind}
        seed={seed}
        suggestion={suggestion}
        billLink={billLink}
        notice={notice}
        accounts={accounts}
        categories={categories}
        tags={tags}
        onDone={onOpenChange}
        onSaved={onSaved}
      />
    </FormDialog>
  )
}

function EditorBody({
  series,
  fixedKind,
  seed,
  suggestion,
  billLink,
  notice,
  accounts,
  categories,
  tags,
  onDone,
  onSaved,
}: {
  series: Series | null
  fixedKind?: SeriesKind
  seed: SeedTransaction | null
  suggestion: SuggestedSeries | null
  billLink: { connectionId: string; subaccountId: string } | null
  notice: string | null
  accounts: readonly { id: string; name: string }[]
  categories: readonly Category[]
  tags: readonly Tag[]
  onDone: (open: boolean) => void
  onSaved?: (series: Series) => void
}) {
  const [edits, setEdits] = useState<SeriesEdits>(() => {
    if (series) return editsFromSeries(series)
    if (suggestion) return editsFromSuggestion(suggestion)
    return blankEdits(seed, fixedKind)
  })
  const create = useCreateSeries()
  const update = useUpdateSeries()
  // Null until somebody picks: the select shows the link the series already
  // carries, and only a choice made here is written.
  const [subaccountChoice, setSubaccountChoice] = useState<string | null>(
    billLink?.subaccountId ?? null,
  )
  const link = useSeriesBillLink(series?.id ?? null)
  const linkSeries = useLinkSeriesToBill()
  const unlink = useUnlinkSeriesFromBill()
  const pending =
    create.isPending || update.isPending || linkSeries.isPending || unlink.isPending
  const error = create.error ?? update.error

  const set = (patch: Partial<SeriesEdits>) => setEdits((current) => ({ ...current, ...patch }))

  const patchSchedule = (next: SchedulePatch) => {
    if (next.recurrence) set({ recurrence: next.recurrence })
    if (next.startOn) set({ start_on: next.startOn })
    if (next.endOn !== undefined) set({ end_on: next.endOn })
  }

  const done = (saved: Series) => {
    onDone(false)
    onSaved?.(saved)
  }

  /**
   * The link is a second write, after the series exists. A link that fails
   * says so and still closes: an open dialog invites a second Save, which on
   * the create path is a second series.
   */
  const saveLink = (saved: Series) => {
    const linked = link.data?.subaccount_id ?? null
    const chosen =
      subaccountChoice === null
        ? linked
        : subaccountChoice === NOT_LINKED
          ? null
          : subaccountChoice
    if (chosen === linked) return done(saved)

    const settle = { onSettled: () => done(saved) }
    if (chosen === null) unlink.mutate(saved.id, settle)
    else linkSeries.mutate({ seriesId: saved.id, subaccountId: chosen }, settle)
  }

  // Shown once a save is tried: a half-typed "1,0" is not a mistake yet.
  const [tried, setTried] = useState(false)
  const amountErrors = tried ? seriesAmountErrors(edits) : {}

  const submit = () => {
    setTried(true)
    if (Object.keys(seriesAmountErrors(edits)).length > 0) return
    if (series) {
      update.mutate({ id: series.id, patch: patchFromEdits(edits) }, { onSuccess: saveLink })
    } else {
      create.mutate(draftFromEdits(edits), { onSuccess: saveLink })
    }
  }

  // The register ranks categories by what is actually on screen; a series
  // editor has no rows to rank, so the picker shows the plain list.
  const frequentIds: readonly Uuid[] = []
  const named = Boolean(edits.name || edits.description)
  // An unbalanced split is refused by the ledger, and finding that out from a
  // rejected request loses whatever was typed.
  const balanced = splitsBalance(edits)

  return (
    <DialogContent
      // A fixed kind names itself. "Create Series" over a form with no Type
      // select leaves the user to work out which of the six they are making.
      title={
        fixedKind
          ? `${series ? 'Edit' : 'New'} ${SERIES_KIND_LABELS[fixedKind].toLowerCase()}`
          : series
            ? 'Edit recurring item'
            : 'New recurring item'
      }
      wide
      onSubmit={submit}
      footer={
        <DialogActions onCancel={() => onDone(false)}>
          <Button
            type="submit"
            variant="primary"
            disabled={pending || !named || !edits.account_id || !edits.amount || !balanced}
          >
            {series ? 'Save' : 'Create'}
          </Button>
        </DialogActions>
      }
    >
      {notice ? <Callout>{notice}</Callout> : null}

      <Tabs defaultValue="basic">
        <TabsList>
          <TabsTrigger value="basic">Basic details</TabsTrigger>
          <TabsTrigger value="frequency">Frequency &amp; occurrence</TabsTrigger>
          <TabsTrigger value="connect">Bill Connect</TabsTrigger>
        </TabsList>

        <TabsContent value="basic">
          <Field label="Name">
            <Input value={edits.name} onChange={(event) => set({ name: event.target.value })} />
          </Field>

          <Field
            label="Matching text"
            hint="Compared with each charge's statement name. Renaming never changes it."
          >
            <Input
              value={edits.description}
              placeholder={edits.name}
              onChange={(event) => set({ description: event.target.value })}
            />
          </Field>

          <div className="form-row">
            <Field label="Recurring amount" error={amountErrors.amount}>
              <MoneyInput
                signed
                value={edits.amount}
                onChange={(event) => set({ amount: event.target.value })}
              />
            </Field>

            <Field label="Match criteria" hint={MATCH_HINTS[edits.match_criteria]}>
              <OptionSelect
                value={edits.match_criteria}
                onValueChange={(value) => set({ match_criteria: asMatch(value) })}
                options={Object.entries(MATCH_LABELS).map(([key, label]) => ({
                  value: key,
                  label,
                }))}
              />
            </Field>
          </div>

          {edits.match_criteria === 'range' ? (
            <div className="form-row">
              <Field label="From" error={amountErrors.match_amount_min}>
                <MoneyInput
                  signed
                  value={edits.match_amount_min}
                  onChange={(event) => set({ match_amount_min: event.target.value })}
                />
              </Field>
              <Field label="To" error={amountErrors.match_amount_max}>
                <MoneyInput
                  signed
                  value={edits.match_amount_max}
                  onChange={(event) => set({ match_amount_max: event.target.value })}
                />
              </Field>
            </div>
          ) : null}

          <div className="form-row">
            <Field label="Account">
              <AccountSelect
                accounts={accounts}
                value={edits.account_id}
                onValueChange={(value) => set({ account_id: value })}
              />
            </Field>

            {fixedKind ? null : (
              <Field label="Type">
                <OptionSelect
                  value={edits.kind}
                  onValueChange={(value) => set({ kind: asKind(value) })}
                  options={kindsFor(edits.kind).map((one) => ({
                    value: one,
                    label: SERIES_KIND_LABELS[one],
                  }))}
                />
              </Field>
            )}
          </div>

          {/* The transaction template: what the charge arrives filed under. A
              split overrides the single category. */}
          <div className="form-row">
            <Field
              label="Category"
              hint={
                edits.splits.length > 0
                  ? 'The split below decides where the money is filed.'
                  : undefined
              }
            >
              <CategoryPicker
                value={edits.category_id}
                categories={categories}
                frequentIds={frequentIds}
                onChange={(id) => set({ category_id: id })}
                trigger={
                  <button type="button" className="select__trigger">
                    {categories.find((one) => one.id === edits.category_id)?.name ??
                      'Uncategorized'}
                  </button>
                }
              />
            </Field>

            <Field label="Tags">
              <TagPicker
                value={edits.tag_ids}
                tags={tags}
                onChange={(ids) => set({ tag_ids: ids })}
                trigger={
                  <button type="button" className="select__trigger">
                    {edits.tag_ids
                      .map((id) => tags.find((tag) => tag.id === id)?.name ?? 'Unknown')
                      .join(', ') || 'Tags'}
                  </button>
                }
              />
            </Field>
          </div>

          {edits.splits.length > 0 ? (
            <Field
              label="Assign splits"
              as="group"
              hint="Set against the recurring amount. A different bill amount is split in the same proportions."
            >
              <SplitEditor
                parent={splitParent(edits)}
                drafts={edits.splits}
                categories={categories}
                frequentCategoryIds={frequentIds}
                saving={pending}
                onChange={(splits) => set({ splits })}
              />
              <Button variant="ghost" onClick={() => set({ splits: [] })}>
                Remove the split
              </Button>
            </Field>
          ) : (
            <Button variant="ghost" onClick={() => set({ splits: startSplitting(edits) })}>
              <SplitIcon size={14} /> Assign splits
            </Button>
          )}
        </TabsContent>

        <TabsContent value="frequency">
          <SeriesSchedule
            recurrence={edits.recurrence}
            startOn={edits.start_on}
            endOn={edits.end_on}
            onChange={patchSchedule}
          />
        </TabsContent>

        <TabsContent value="connect">
          <BillLink
            value={subaccountChoice ?? link.data?.subaccount_id ?? NOT_LINKED}
            linkedConnectionId={link.data?.connection_id ?? billLink?.connectionId ?? null}
            onChange={setSubaccountChoice}
          />

          <Switch
            label="Move the due date to the one the biller asks for"
            checked={edits.auto_adjust_due_on}
            onCheckedChange={(checked) => set({ auto_adjust_due_on: checked })}
          />
          <p className="muted">
            Each reminder takes its amount from the biller’s statement for it; a one-off
            amount you type still wins. Leave this off to keep the due day fixed.
          </p>

          <Field
            label="Remind me this many days before"
            hint={
              series
                ? 'Leave empty to keep the lead time this item already has.'
                : 'Three days when left empty.'
            }
          >
            <Input
              type="number"
              min={0}
              numeric
              value={edits.reminder_days}
              onChange={(event) => set({ reminder_days: event.target.value })}
            />
          </Field>

          {series ? (
            <Field
              label="Only the next one"
              as="group"
              hint="The next occurrence only. The schedule and recurring amount are unchanged."
            >
              <div className="form-row">
                <Field label="Amount" error={amountErrors.override_next_amount}>
                  <MoneyInput
                    signed
                    placeholder={edits.amount}
                    value={edits.override_next_amount}
                    onChange={(event) => set({ override_next_amount: event.target.value })}
                  />
                </Field>
                <Field label="Date">
                  <Input
                    type="date"
                    value={edits.override_next_due_on ?? ''}
                    onChange={(event) =>
                      set({ override_next_due_on: event.target.value || null })
                    }
                  />
                </Field>
              </div>
              <Button
                variant="ghost"
                disabled={!edits.override_next_amount && edits.override_next_due_on === null}
                onClick={() => set({ override_next_amount: '', override_next_due_on: null })}
              >
                Clear the override
              </Button>
            </Field>
          ) : null}
        </TabsContent>
      </Tabs>

      {error ? <p className="field__error">{describeApiError(error)}</p> : null}
    </DialogContent>
  )
}

/** Radix has no empty option value, so "linked to nothing" needs a word of its own. */
const NOT_LINKED = 'none'

/**
 * Which bill this reminder follows: a login, then one of the things that login
 * bills for (a provider account lists every premise or card on it).
 *
 * A listing that fails leaves the controls drawn and disabled with the same
 * note a household with nothing connected gets, which says where a bill comes
 * from.
 */
function BillLink({
  value,
  linkedConnectionId,
  onChange,
}: {
  /** The subaccount this reminder follows, or `NOT_LINKED`. */
  value: string
  linkedConnectionId: string | null
  onChange: (subaccountId: string) => void
}) {
  const connections = useBillConnections()
  const [connectionChoice, setConnectionChoice] = useState<string | null>(null)
  const [addingProvider, setAddingProvider] = useState(false)
  const followUp = useProviderFollowUp()
  const connectionValue = connectionChoice ?? linkedConnectionId ?? NOT_LINKED
  const connectionId = connectionValue === NOT_LINKED ? null : connectionValue
  const subaccounts = useBillSubaccounts(connectionId)

  const offline = connections.isError || subaccounts.isError
  const connectionList = offline ? [] : (connections.data ?? [])
  // A hidden account is not offered, except the one this reminder already
  // follows: hiding it must not silently unlink a series.
  const subaccountList = (offline ? [] : (subaccounts.data ?? [])).filter(
    (one) => one.is_selected || one.id === value,
  )

  return (
    <Field
      label="Bill connection"
      as="group"
      hint="The provider account this reminder follows."
    >
      <div className="form-row">
        <Field label="Provider">
          <OptionSelect
            value={connectionValue}
            disabled={offline || connectionList.length === 0}
            onValueChange={(next) => {
              setConnectionChoice(next)
              // The subaccounts belong to the previous login; keeping the old
              // one would link to another account's bill.
              onChange(NOT_LINKED)
            }}
            options={[
              { value: NOT_LINKED, label: 'Not connected' },
              ...connectionList.map((one) => ({ value: one.id, label: connectionTitle(one) })),
            ]}
          />
        </Field>

        <Field label="Account">
          <OptionSelect
            value={value}
            disabled={offline || connectionId === null || subaccountList.length === 0}
            onValueChange={onChange}
            options={[
              { value: NOT_LINKED, label: 'Not linked' },
              ...subaccountList.map((one) => ({ value: one.id, label: one.label })),
            ]}
          />
        </Field>
      </div>

      {/* Not while the listing is still in the air: a note that says there are
          none, a moment before the list arrives, is a worse answer than none. */}
      {offline || connections.data?.length === 0 ? (
        <EmptyState
          compact
          title="No bill connections yet. Add the company that bills you to follow its bills here."
        />
      ) : null}
      {offline ? null : (
        <Button size="sm" variant="ghost" onClick={() => setAddingProvider(true)}>
          <Plus size={13} aria-hidden="true" /> Add a bill provider
        </Button>
      )}
      {addingProvider ? (
        <ConnectionDialog
          connection={null}
          onClose={() => setAddingProvider(false)}
          onCreated={(created) => {
            setConnectionChoice(created.id)
            onChange(NOT_LINKED)
            followUp.next(created)
          }}
        />
      ) : null}
      {followUp.dialog}
    </Field>
  )
}

function blankEdits(seed: SeedTransaction | null, fixedKind?: SeriesKind): SeriesEdits {
  const startOn = seed?.date ?? toIsoDate(new Date())
  // A refund is one expected credit: monthly would advance into next month
  // rather than closing it.
  const oneOff = fixedKind === 'refund'
  return {
    name: seed?.payee ?? '',
    // The bank's raw string when there is one, and never overwritten by a
    // later rename.
    description: seed?.statement_name ?? '',
    amount: seed?.amount ?? '0.00',
    account_id: seed?.account_id ?? '',
    category_id: seed?.category_id ?? null,
    kind: fixedKind ?? 'bill',
    recurrence: recurrenceFor(oneOff ? 'ONE_TIME' : 'EVERY_MONTH', new Date(`${startOn}T00:00:00`)),
    start_on: startOn,
    end_on: null,
    match_criteria: 'auto',
    match_amount_min: '',
    match_amount_max: '',
    auto_adjust_due_on: false,
    reminder_days: '',
    // A series that does not exist yet has no next slot to override.
    override_next_due_on: null,
    override_next_amount: '',
    // A blank series carries no template: an empty split grid and a split
    // nobody has filled in yet are not the same thing.
    tag_ids: [],
    splits: [],
  }
}

function asMatch(value: string): MatchCriteria {
  return value === 'any' || value === 'exact' || value === 'range' ? value : 'auto'
}

/**
 * The five the Type select offers. Not refund: the Refunds tab fixes that kind
 * and hides the select.
 */
const SERIES_KINDS: readonly SeriesKind[] = [
  'bill',
  'subscription',
  'income',
  'transfer',
  'credit_card_payment',
]

/**
 * What the select shows for an existing series. A kind outside the five is
 * added, since a select whose value is not an option renders empty.
 */
function kindsFor(current: SeriesKind): readonly SeriesKind[] {
  return SERIES_KINDS.includes(current) ? SERIES_KINDS : [current, ...SERIES_KINDS]
}

/**
 * A select value as a kind, over every kind the wire knows: coercing a refund
 * to `bill` would re-type the series.
 */
function asKind(value: string): SeriesKind {
  return ALL_KINDS.find((one) => one === value) ?? 'bill'
}

const ALL_KINDS: readonly SeriesKind[] = [
  'bill',
  'subscription',
  'income',
  'transfer',
  'credit_card_payment',
  'refund',
]
