import { useState } from 'react'

import { CategoryChecklist } from '@/components/CategoryChecklist'
import { Facts } from '@/components/Facts'
import { Money } from '@/components/Money'
import {
  Button,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  Input,
  MoneyInput,
  Radio,
  RadioGroup,
  Switch,
  useHeld,
} from '@/components/ui'
import {
  envelopeAvailable,
  envelopeTarget,
  releaseRollover,
  type Envelope,
  type OtherSpendSlice,
} from '@/lib/spendingPlan'
import { formatMonthKey } from '@/lib/format'
import { absMoney, amountToWire, parseAmountInput } from '@/lib/money'

export interface CategoryOption {
  id: string
  name: string
  /** Null, or a parent outside the offered set, reads as top level. */
  parent_id: string | null
}

export interface AddPlannedExpenseDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  month: string
  categories: CategoryOption[]
  onCreate: (body: {
    name: string
    target_amount: string
    category_ids: string[]
    recurring: boolean
    rollover: boolean
  }) => void
}

/** The name an unnamed envelope takes: one category's name, or a count of several. */
function defaultName(categories: readonly CategoryOption[], chosen: readonly string[]): string {
  if (chosen.length === 1) {
    return categories.find((category) => category.id === chosen[0])?.name ?? 'Planned spend'
  }
  return chosen.length === 0 ? 'Planned spend' : `${chosen.length} categories`
}

/**
 * *Add Planned Expense*. The amount leaves as a string: a float in the payload
 * has lost precision before the server's `Decimal` parse sees it.
 */
export function AddPlannedExpenseDialog({
  open,
  onOpenChange,
  month,
  categories,
  onCreate,
}: AddPlannedExpenseDialogProps) {
  return (
    <FormDialog open={open} onOpenChange={onOpenChange}>
      <AddPlannedExpenseForm
        onOpenChange={onOpenChange}
        month={month}
        categories={categories}
        onCreate={onCreate}
      />
    </FormDialog>
  )
}

function AddPlannedExpenseForm({
  onOpenChange,
  month,
  categories,
  onCreate,
}: Omit<AddPlannedExpenseDialogProps, 'open'>) {
  const [recurring, setRecurring] = useState(true)
  const [name, setName] = useState('')
  const [amount, setAmount] = useState('')
  const [categoryIds, setCategoryIds] = useState<string[]>([])
  const [rollover, setRollover] = useState(false)

  // Normalized here rather than sent raw: "$1,900" and "1,900.5" are typed all
  // the time, and the wire wants one decimal string.
  const parsedAmount = parseAmountInput(amount)

  const submit = () => {
    if (!parsedAmount || categoryIds.length === 0) return
    onCreate({
      name: name || defaultName(categories, categoryIds),
      target_amount: parsedAmount.wire,
      category_ids: categoryIds,
      recurring,
      rollover,
    })
    onOpenChange(false)
  }

  return (
    <DialogContent
      title="New planned expense"
      description="Budget your spend by category. Its transactions leave Other Spend."
      onSubmit={submit}
      footer={
        <DialogActions>
          <Button
            type="submit"
            variant="primary"
            disabled={!parsedAmount || categoryIds.length === 0}
          >
            Create
          </Button>
        </DialogActions>
      }
    >
      <RadioGroup
        value={recurring ? 'recurring' : 'one_month'}
        onValueChange={(value) => setRecurring(value === 'recurring')}
      >
        <Radio value="recurring" label="Recurring expense" />
        <Radio value="one_month" label={`${formatMonthKey(month, 'month')} only`} />
      </RadioGroup>

      <Field label="Name (optional)">
        <Input value={name} onChange={(event) => setName(event.target.value)} />
      </Field>

      <Field label="Target amount">
        <MoneyInput
          placeholder="0.00"
          value={amount}
          onChange={(event) => setAmount(event.target.value)}
        />
      </Field>

      <Field
        label="Categories"
        as="group"
        hint="Every transaction filed under one of these counts against the target."
      >
        <CategoryChecklist
          categories={categories}
          selected={categoryIds}
          onChange={setCategoryIds}
          emptyLabel="No spending categories yet."
        />
      </Field>

      <Switch
        checked={rollover}
        onCheckedChange={setRollover}
        label="Roll remaining balance over each month"
        labelPosition="before"
      />
    </DialogContent>
  )
}

export type EnvelopeEditMode = 'release' | 'rollover' | 'target'

export interface EnvelopeEditDialogProps {
  envelope: Envelope | null
  mode: EnvelopeEditMode
  onOpenChange: (open: boolean) => void
  onRelease: (envelope: Envelope) => void
  /** Sets either the carried amount or this month's target, per `mode`. */
  onSetAmount: (envelope: Envelope, mode: EnvelopeEditMode, amount: string) => void
  onAutoRelease: (envelope: Envelope, on: boolean) => void
}

/**
 * The rollover operations (release now, set, release monthly) and this
 * month's target. Release names the figure it is about to move back to
 * free-to-spend.
 */
export function EnvelopeEditDialog({
  envelope: openEnvelope,
  mode: openMode,
  onOpenChange,
  ...rest
}: EnvelopeEditDialogProps) {
  const envelope = useHeld(openEnvelope)
  const mode = useHeld(openEnvelope === null ? null : openMode)
  return (
    <FormDialog open={openEnvelope !== null} onOpenChange={onOpenChange}>
      {envelope === null || mode === null ? null : (
        <EnvelopeEditForm envelope={envelope} mode={mode} {...rest} />
      )}
    </FormDialog>
  )
}

function EnvelopeEditForm({
  envelope,
  mode,
  onRelease,
  onSetAmount,
  onAutoRelease,
}: Omit<EnvelopeEditDialogProps, 'envelope' | 'onOpenChange'> & { envelope: Envelope }) {
  const [amount, setAmount] = useState('')
  const parsedAmount = parseAmountInput(amount)

  const { released } = releaseRollover(envelope)

  const submit = () => {
    if (mode === 'release') {
      onRelease(envelope)
      return
    }
    if (!parsedAmount) return
    onSetAmount(envelope, mode, parsedAmount.wire)
  }

  return (
    <DialogContent
      title={
        mode === 'release'
          ? 'Release unspent funds'
          : mode === 'target'
            ? "Edit this month's expense"
            : 'Change rollover amount'
      }
      description={envelope.name}
      onSubmit={submit}
      footer={
        <DialogActions>
          {mode === 'release' ? (
            <Button type="submit" variant="primary">
              Release <Money value={released} signs="absolute" tone="neutral" />
            </Button>
          ) : (
            <Button type="submit" variant="primary" disabled={!parsedAmount}>
              Save
            </Button>
          )}
        </DialogActions>
      }
    >
      {mode === 'release' ? (
        <>
          <Facts
            facts={[
              {
                label: 'Back to free-to-spend',
                value: <Money value={released} signs="absolute" tone="neutral" />,
              },
              {
                label: 'Available now',
                value: (
                  <Money value={envelopeAvailable(envelope)} signs="absolute" tone="neutral" />
                ),
              },
              {
                label: 'Available after',
                value: (
                  <Money
                    value={envelopeAvailable(releaseRollover(envelope).envelope)}
                    signs="absolute"
                    tone="neutral"
                  />
                ),
              },
            ]}
          />
          <p className="muted">The target is unchanged.</p>
        </>
      ) : mode === 'target' ? (
        <Field
          label="Target amount"
          hint="This month only. Next month goes back to the usual target."
        >
          <MoneyInput
            placeholder={amountToWire(absMoney(envelopeTarget(envelope)))}
            value={amount}
            onChange={(event) => setAmount(event.target.value)}
          />
        </Field>
      ) : (
        <Field
          label="Rollover amount"
          hint="What this envelope carried in. Overrides the calculated figure."
        >
          <MoneyInput
            placeholder={amountToWire(absMoney(envelope.rollover_amount))}
            value={amount}
            onChange={(event) => setAmount(event.target.value)}
          />
        </Field>
      )}

      <Switch
        checked={envelope.auto_release_rollover}
        onCheckedChange={(on) => onAutoRelease(envelope, on)}
        label="Release unspent funds automatically every month"
        labelPosition="before"
      />
    </DialogContent>
  )
}

export interface EnvelopeCategoriesDialogProps {
  envelope: Envelope | null
  month: string
  categories: CategoryOption[]
  onOpenChange: (open: boolean) => void
  onSave: (
    envelope: Envelope,
    patch: { name: string; category_ids: string[]; recurring: boolean },
  ) => void
}

/**
 * *Edit expense series*: what the envelope is, not what this month did, so it
 * is apart from the one-month overrides. The name travels with the categories,
 * and both apply to every month the envelope appears in, which the dialog says.
 *
 * An empty selection is refused: a filter with no items matches everything,
 * so the envelope would claim the whole ledger.
 */
export function EnvelopeCategoriesDialog({
  envelope: openEnvelope,
  onOpenChange,
  ...rest
}: EnvelopeCategoriesDialogProps) {
  const envelope = useHeld(openEnvelope)
  return (
    <FormDialog open={openEnvelope !== null} onOpenChange={onOpenChange}>
      {envelope === null ? null : <EnvelopeCategoriesForm envelope={envelope} {...rest} />}
    </FormDialog>
  )
}

function EnvelopeCategoriesForm({
  envelope,
  month,
  categories,
  onSave,
}: Omit<EnvelopeCategoriesDialogProps, 'envelope' | 'onOpenChange'> & { envelope: Envelope }) {
  const [name, setName] = useState(envelope.name)
  const [recurring, setRecurring] = useState(envelope.recurring)
  const [categoryIds, setCategoryIds] = useState<string[]>(() =>
    envelope.categories.map((category) => category.id),
  )

  const trimmed = name.trim()
  const ready = trimmed !== '' && categoryIds.length > 0

  return (
    <DialogContent
      title="Edit expense series"
      description={envelope.name}
      onSubmit={() => {
        if (!ready) return
        onSave(envelope, { name: trimmed, category_ids: categoryIds, recurring })
      }}
      footer={
        <DialogActions>
          <Button type="submit" variant="primary" disabled={!ready}>
            Save
          </Button>
        </DialogActions>
      }
    >
      <p className="dialog__prose">
        Applies to every month the envelope appears in, unlike the target and rollover.
      </p>

      <RadioGroup
        value={recurring ? 'recurring' : 'one_month'}
        onValueChange={(value) => setRecurring(value === 'recurring')}
      >
        <Radio value="recurring" label="Recurring expense" />
        <Radio value="one_month" label={`${formatMonthKey(month, 'month')} only`} />
      </RadioGroup>

      <Field label="Name">
        <Input value={name} onChange={(event) => setName(event.target.value)} />
      </Field>

      <Field
        label="Categories"
        as="group"
        hint="Every transaction filed under one of these counts against the target."
      >
        <CategoryChecklist
          categories={categories}
          selected={categoryIds}
          onChange={setCategoryIds}
          emptyLabel="No spending categories yet."
        />
      </Field>
    </DialogContent>
  )
}

export interface AddToPlannedSpendDialogProps {
  /** The bubble being converted; null keeps the dialog closed. */
  slice: OtherSpendSlice | null
  onOpenChange: (open: boolean) => void
  onConfirm: (slice: OtherSpendSlice, targetAmount: string) => void
}

/**
 * *Add to Planned Spend*, from the Other Spend chart: one step, with the target
 * starting at what the category has spent this month. The new envelope claims
 * the category, and the recalculated month files its transactions under
 * Planned Spend.
 */
export function AddToPlannedSpendDialog({
  slice: openSlice,
  onOpenChange,
  onConfirm,
}: AddToPlannedSpendDialogProps) {
  const slice = useHeld(openSlice)
  return (
    <FormDialog open={openSlice !== null} onOpenChange={onOpenChange}>
      {slice === null ? null : (
        <AddToPlannedSpendForm slice={slice} onOpenChange={onOpenChange} onConfirm={onConfirm} />
      )}
    </FormDialog>
  )
}

function AddToPlannedSpendForm({
  slice,
  onOpenChange,
  onConfirm,
}: Omit<AddToPlannedSpendDialogProps, 'slice'> & { slice: OtherSpendSlice }) {
  const [amount, setAmount] = useState(() => amountToWire(absMoney(slice.spent)))
  const parsedAmount = parseAmountInput(amount)

  const submit = () => {
    if (!parsedAmount) return
    onConfirm(slice, parsedAmount.wire)
    onOpenChange(false)
  }

  return (
    <DialogContent
      title="Add to Planned Spend"
      description={`Move ${slice.category_name} to Planned Spend? Its transactions count against a target you set.`}
      onSubmit={submit}
      footer={
        <DialogActions>
          <Button type="submit" variant="primary" disabled={!parsedAmount}>
            Confirm
          </Button>
        </DialogActions>
      }
    >
      <Field label="Target amount" hint="Started at what it has spent this month.">
        <MoneyInput
          placeholder="0.00"
          value={amount}
          onChange={(event) => setAmount(event.target.value)}
        />
      </Field>
    </DialogContent>
  )
}
