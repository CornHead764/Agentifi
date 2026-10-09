import {
  Check,
  ChevronDown,
  CircleCheck,
  ExternalLink,
  Eye,
  Flag,
  Pencil,
  PiggyBank,
  Repeat,
  Sparkles,
  Undo2,
  Split as SplitIcon,
  Wand2,
  X,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

import { useMoneyText } from '@/components/moneyText'
import {
  Button,
  Checkbox,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  Input,
  MoneyInput,
  Spinner,
  Textarea,
  Tooltip,
} from '@/components/ui'
import { usePrivacy } from '@/contexts/privacy'
import { defaultTargetAccountId, manualTransactionTargets } from '@/lib/accounts'
import { categoryLabel } from '@/lib/categoryNames'
import { CURRENCIES } from '@/lib/currencies'
import { formatDate, toIsoDate } from '@/lib/format'
import { ZERO_MONEY, amountToWire, parseAmountInput, type MoneyFormatter } from '@/lib/money'
import { isUndetermined } from '@/lib/transactions/categoryCheck'
import { openingCurrency } from '@/lib/transactions/edits'
import {
  describeProblem,
  initialDrafts,
  validateSplits,
  type SplitDraft,
} from '@/lib/transactions/splits'
import { describeSuggestion, suggestedCategoryId } from '@/lib/transactions/suggestions'
import type {
  Account,
  Category,
  Suggestion,
  Tag,
  Transaction,
  TransactionCreate,
  Uuid,
} from '@/lib/transactions/types'

import { MerchantPanel } from './MerchantPanel'
import { RefundLinksPanel } from './RefundLinksPanel'
import { AttachmentPanel } from './AttachmentPanel'
import { GoalPanel } from './GoalPanel'
import { AccountPicker, CategoryPicker, TagPicker } from './Pickers'
import { SplitEditor } from './SplitEditor'
import { AskRowButton } from '@/components/assistant/AskMenuItem'
import { transactionSubject } from '@/lib/assistant/subjects'

export interface TransactionDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Null opens the create form; a row opens the detail view of it. */
  transaction: Transaction | null
  accounts: readonly Account[]
  categories: readonly Category[]
  tags: readonly Tag[]
  frequentCategoryIds: readonly Uuid[]
  defaultAccountId: Uuid | null
  /** The space's primary currency, for a new row on no account in particular. */
  spaceCurrency: string | null
  saving: boolean
  onCreate: (body: TransactionCreate) => void
  /** The body and the same change in cache shape: the wire carries an amount as a string, the cache as `Money`. */
  onUpdate: (id: Uuid, patch: Record<string, unknown>, optimistic: Partial<Transaction>) => void
  onDelete: (id: Uuid) => void
  onInvalid: (message: string) => void
  /** Open the rule or series builder on this row. The page owns both editors, since either replaces this dialog. */
  onCreateRule: (txn: Transaction) => void
  onCreateSeries: (txn: Transaction) => void
  onTrackRefund: (txn: Transaction) => void
  /** Open the purchase picker on this row. The page owns that dialog too, so
   *  the register's row menu and this detail open the one mount of it. */
  onMerchantOrder: (txn: Transaction) => void
  onSuggestCategory?: (txn: Transaction) => void
  suggestingCategory?: boolean
  /** Open another row in this dialog: the income row padding a purchase, or the purchase a pad belongs to. */
  onOpenRow?: (id: Uuid) => void
  /** Present when the row has a pending suggestion; it is shown above the form. */
  review?: ReviewFlow | null
}

/**
 * The decisions on what the assistant proposed for the row:
 *
 *   - **Approve** applies the proposal through the assistant's path, which
 *     ticks the row reviewed.
 *   - **Use my category** applies it with the chosen category instead; the
 *     server records the disagreement for the next run about this payee.
 *   - **Discard** settles the card and leaves the row unreviewed.
 */
export interface ReviewFlow {
  suggestion: Suggestion
  /** A decision is in flight; the buttons stop taking clicks. */
  busy: boolean
  onApprove: (splitOverrides?: { index: number; category_id: string }[]) => void
  onUseMine: (categoryId: Uuid | null) => void
  onDiscard: () => void
  /** Open the automation run that proposed this, in full. */
  onShowRun: (runId: Uuid) => void
}

export function TransactionDialog(props: TransactionDialogProps) {
  const { open, onOpenChange, transaction } = props
  // The submit button is in `TransactionForm`, so Enter-to-submit goes
  // through a ref.
  const submitRef = useRef(() => {})

  return (
    <FormDialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        title={transaction ? 'Transaction detail' : 'Create transaction'}
        wide
        onSubmit={() => submitRef.current()}
      >
        <TransactionForm key={transaction?.id ?? 'new'} {...props} submitRef={submitRef} />
      </DialogContent>
    </FormDialog>
  )
}

interface FormState {
  payee: string
  date: string
  /** Empty means "the same as the date", which is what the server stores as null. */
  effectiveDate: string
  amount: string
  /** ISO 4217. Defaults to the account's, which is the household's in all but
   *  the occasional foreign charge. */
  currency: string
  /** Whether the currency was picked rather than inherited from the account; a picked one survives an account change. */
  currencyTouched: boolean
  accountId: Uuid
  categoryId: Uuid | null
  tagIds: Uuid[]
  notes: string
  checkNumber: string
  isPending: boolean
  isReviewed: boolean
  flagged: boolean
  flagNote: string
  excludedFromReports: boolean
  excludedFromSpendingPlan: boolean
  receiptNotNeeded: boolean
}

function initialState(
  transaction: Transaction | null,
  defaultAccountId: Uuid | null,
  accounts: readonly Account[],
  spaceCurrency: string | null,
): FormState {
  if (transaction) {
    return {
      payee: transaction.payee,
      date: transaction.date,
      effectiveDate: transaction.effective_date ?? '',
      amount: amountToWire(transaction.amount),
      currency: transaction.currency,
      // The row was entered in a currency somebody already settled on.
      currencyTouched: true,
      accountId: transaction.account_id,
      categoryId: transaction.category_id,
      tagIds: transaction.tag_ids,
      notes: transaction.notes ?? '',
      checkNumber: transaction.check_number ?? '',
      isPending: transaction.is_pending,
      isReviewed: transaction.is_reviewed,
      flagged: transaction.user_flag !== null,
      flagNote: transaction.user_flag_note ?? '',
      excludedFromReports: transaction.excluded_from_reports,
      excludedFromSpendingPlan: transaction.excluded_from_spending_plan,
      receiptNotNeeded: transaction.receipt_not_needed,
    }
  }
  // The account being looked at when it can take a row, else the first that can.
  const accountId = defaultTargetAccountId(accounts, defaultAccountId)
  return {
    payee: '',
    date: toIsoDate(new Date()),
    effectiveDate: '',
    amount: '0.00',
    currency: openingCurrency(accountId, accounts, spaceCurrency),
    currencyTouched: false,
    accountId,
    categoryId: null,
    tagIds: [],
    notes: '',
    checkNumber: '',
    isPending: false,
    isReviewed: false,
    flagged: false,
    flagNote: '',
    excludedFromReports: false,
    excludedFromSpendingPlan: false,
    receiptNotNeeded: false,
  }
}

function TransactionForm({
  transaction,
  accounts,
  categories,
  tags,
  frequentCategoryIds,
  defaultAccountId,
  spaceCurrency,
  saving,
  onCreate,
  onUpdate,
  onDelete,
  onInvalid,
  onCreateRule,
  onCreateSeries,
  onTrackRefund,
  onMerchantOrder,
  onSuggestCategory,
  suggestingCategory,
  onOpenRow,
  onOpenChange,
  review,
  submitRef,
}: TransactionDialogProps & { submitRef: RefObject<() => void> }) {
  const [form, setForm] = useState(() =>
    initialState(transaction, defaultAccountId, accounts, spaceCurrency),
  )
  // Null while the splits are not being edited. The edits are part of the form
  // and leave with Update, not on a save of their own.
  const [drafts, setDrafts] = useState<SplitDraft[] | null>(null)
  const splitting = drafts !== null
  // Editing inline would put the digits back on screen — the one thing
  // privacy mode exists to prevent. Same rule the register's amount cell uses.
  const { hidden } = usePrivacy()
  const moneyText = useMoneyText()

  const account = accounts.find((row) => row.id === form.accountId)
  const category = categories.find((row) => row.id === form.categoryId)
  const hasSplits = (transaction?.splits.length ?? 0) > 0

  // A row not yet split is allocated against the amount as typed, which is
  // what the server will check the parts against.
  const splitParent =
    transaction === null || hasSplits
      ? (transaction?.amount ?? ZERO_MONEY)
      : (parseAmountInput(form.amount)?.cents ?? transaction.amount)
  const splitCheck = drafts === null ? null : validateSplits(drafts, splitParent)
  const splitBlock =
    splitCheck !== null && splitCheck.rows === null
      ? splitCheck.problems.map(describeProblem).join(' ')
      : null

  const patch = (change: Partial<FormState>) => setForm((current) => ({ ...current, ...change }))

  // Approving a suggestion changes the row under the form; the form follows,
  // so a later save does not put the old category back.
  const saved = `${transaction?.category_id ?? ''}|${transaction?.is_reviewed ?? ''}`
  const [seen, setSeen] = useState(saved)
  if (transaction && seen !== saved) {
    setSeen(saved)
    patch({ categoryId: transaction.category_id, isReviewed: transaction.is_reviewed })
  }

  const submit = () => {
    const parsed = parseAmountInput(form.amount)
    if (parsed === null) {
      onInvalid(`"${form.amount}" is not an amount.`)
      return
    }
    if (transaction === null) {
      onCreate({
        account_id: form.accountId,
        date: form.date,
        effective_date: form.effectiveDate || null,
        amount: parsed.wire,
        currency: form.currency,
        payee: form.payee,
        notes: form.notes || null,
        check_number: form.checkNumber || null,
        category_id: form.categoryId,
        is_pending: form.isPending,
        is_reviewed: form.isReviewed,
        excluded_from_reports: form.excludedFromReports,
        excluded_from_spending_plan: form.excludedFromSpendingPlan,
        user_flag: form.flagged ? 'flagged' : null,
        user_flag_note: form.flagged ? form.flagNote || null : null,
        tag_ids: form.tagIds,
      })
      return
    }
    if (splitCheck !== null && splitCheck.rows === null) {
      onInvalid(splitBlock ?? 'The splits are not complete.')
      return
    }
    // A split row's category lives on its splits; the server clears the
    // parent's when it files them.
    const categoryField = splitCheck === null ? { category_id: form.categoryId } : {}
    const amount = hasSplits ? amountToWire(transaction.amount) : parsed.wire
      // Only when changed: the server re-derives the currency from a new
      // account precisely when the field is not sent.
    const currency = form.currency === transaction.currency ? {} : { currency: form.currency }
    onUpdate(
      transaction.id,
      {
        account_id: form.accountId,
        date: form.date,
        effective_date: form.effectiveDate || null,
        ...currency,
        // The server refuses a split row's amount; the field is disabled, so
        // this resends the original.
        amount,
        payee: form.payee,
        notes: form.notes || null,
        check_number: form.checkNumber || null,
        ...categoryField,
        is_pending: form.isPending,
        is_reviewed: form.isReviewed,
        excluded_from_reports: form.excludedFromReports,
        excluded_from_spending_plan: form.excludedFromSpendingPlan,
        user_flag: form.flagged ? 'flagged' : null,
        user_flag_note: form.flagged ? form.flagNote || null : null,
        receipt_not_needed: form.receiptNotNeeded,
        tag_ids: form.tagIds,
        ...(splitCheck?.rows ? { splits: splitCheck.rows } : {}),
      },
      {
        account_id: form.accountId,
        date: form.date,
        effective_date: form.effectiveDate || null,
        ...currency,
        amount: hasSplits ? transaction.amount : parsed.cents,
        payee: form.payee,
        notes: form.notes || null,
        check_number: form.checkNumber || null,
        ...categoryField,
        is_pending: form.isPending,
        is_reviewed: form.isReviewed,
        excluded_from_reports: form.excludedFromReports,
        excluded_from_spending_plan: form.excludedFromSpendingPlan,
        user_flag: form.flagged ? 'flagged' : null,
        user_flag_note: form.flagged ? form.flagNote || null : null,
        receipt_not_needed: form.receiptNotNeeded,
        tag_ids: form.tagIds,
      },
    )
  }

  // Assigned after every render so the dialog's form always calls the latest
  // closure, without making the ref itself a render dependency.
  useEffect(() => {
    submitRef.current = submit
  })

  return (
    <>
      {review && transaction ? (
        <SuggestionBanner
          review={review}
          categories={categories}
          frequentCategoryIds={frequentCategoryIds}
        />
      ) : null}

      {transaction ? (
        <p className="txn-provenance">
          Appears on your {account?.name ?? 'account'} statement as{' '}
          <strong>{transaction.statement_name || 'an unnamed charge'}</strong> on{' '}
          {formatDate(transaction.date)}
          {/* The day it was swiped, only when it differs from the posted day. */}
          {transaction.transacted_on ? <> (made {formatDate(transaction.transacted_on)})</> : null}.{' '}
          <Tooltip
            label="The bank's wording. Rules and recurring series match on it, so it is never rewritten."
            side="top"
          >
            <span className="hint">
              <ExternalLink
                size={11}
                aria-label="The bank's wording. Rules and recurring series match on it, so it is never rewritten."
              />
            </span>
          </Tooltip>
          {/* The bank's second line. Not in Notes, which is the household's
              own and would be overwritten by an edit. */}
          {transaction.memo ? (
            <>
              <br />
              Memo: <strong>{transaction.memo}</strong>
            </>
          ) : null}
          <PaddingNote transaction={transaction} onOpenRow={onOpenRow} />
        </p>
      ) : null}

      {/* Toggles, not submits: set is shown as the accent on the same outline,
          not the filled primary the Save button wears. */}
      <div className="row row--wrap txn-actions txn-actions--toggles">
        <Button
          variant="secondary"
          size="sm"
          aria-pressed={form.isReviewed}
          onClick={() => patch({ isReviewed: !form.isReviewed })}
        >
          <CircleCheck size={14} /> {form.isReviewed ? 'Reviewed' : 'Mark reviewed'}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          aria-pressed={form.flagged}
          onClick={() => patch({ flagged: !form.flagged })}
        >
          <Flag size={14} /> {form.flagged ? 'Flagged' : 'Flag'}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          aria-pressed={form.isPending}
          onClick={() => patch({ isPending: !form.isPending })}
        >
          {form.isPending ? 'Pending' : 'Posted'}
        </Button>
      </div>

      {/* Only once the row exists: a rule matches the bank's wording and a
          series is detected from history, and an unsaved form has neither. */}
      {transaction ? (
        <div className="row row--wrap txn-actions">
          <Button variant="ghost" size="sm" onClick={() => onCreateRule(transaction)}>
            <Wand2 size={14} /> Create a rule…
          </Button>
          <Button variant="ghost" size="sm" onClick={() => onCreateSeries(transaction)}>
            <Repeat size={14} /> Create a series…
          </Button>
          <Button variant="ghost" size="sm" onClick={() => onTrackRefund(transaction)}>
            <Undo2 size={14} /> Track a refund…
          </Button>
          <AskRowButton subject={() => transactionSubject(transaction)} />
        </div>
      ) : null}

      <div className="txn-form">
        <Field label="Payee" className="txn-form__full">
          <Input
            value={form.payee}
            placeholder={transaction?.statement_name ?? 'Select payee'}
            onChange={(event) => patch({ payee: event.target.value })}
          />
        </Field>

        <Field label="Date">
          <Input
            type="date"
            value={form.date}
            onChange={(event) => patch({ date: event.target.value })}
          />
        </Field>

        {/* Trap 4: `effective_date` is the reporting date, and an empty one
            means the row reports on its own date. */}
        <Field
          label="Effective date"
          hint="Optional. Reports and the plan use this date instead."
        >
          <Input
            type="date"
            value={form.effectiveDate}
            onChange={(event) => patch({ effectiveDate: event.target.value })}
          />
        </Field>

        <Field label="Amount" hint={amountHint(hasSplits, transaction, moneyText)}>
          <div className="row amount-field">
            <MoneyInput
              signed
              currency={form.currency}
              value={form.amount}
              disabled={hasSplits || hidden}
              onChange={(event) => patch({ amount: event.target.value })}
            />
            {/* A native select: the list is short, it needs no search, and a
                currency is chosen once in a hundred entries. */}
            <select
              className="amount-field__currency"
              value={form.currency}
              aria-label="Currency"
              onChange={(event) => patch({ currency: event.target.value, currencyTouched: true })}
            >
              {CURRENCIES.map((one) => (
                <option key={one.code} value={one.code}>
                  {one.code}
                </option>
              ))}
              {CURRENCIES.every((one) => one.code !== form.currency) ? (
                <option value={form.currency}>{form.currency}</option>
              ) : null}
            </select>
          </div>
        </Field>

        <Field label="Account">
          {/* Only accounts a hand-entered row belongs in, plus the row's own,
              so saving cannot move it. */}
          <AccountPicker
            value={form.accountId}
            accounts={manualTransactionTargets(accounts, transaction?.account_id)}
            onChange={(id) =>
              patch(
                form.currencyTouched
                  ? { accountId: id }
                  : { accountId: id, currency: openingCurrency(id, accounts, spaceCurrency) },
              )
            }
            trigger={
              <button type="button" className="select__trigger">
                {account?.name ?? 'Select account'}
              </button>
            }
          />
        </Field>

        {/* The reason a check came back with nothing, which the cell can only hint at. */}
        <Field
          label="Category"
          hint={
            transaction && isUndetermined(transaction)
              ? `The assistant could not place this row. ${transaction.category_check_note}`
              : undefined
          }
        >
          <div className="row txn-form__category">
            {/* A split row's categories are its splits'; its parent has none to pick. */}
            {transaction && hasSplits ? (
              <button
                type="button"
                className="select__trigger"
                disabled={splitting}
                onClick={() => setDrafts(initialDrafts(transaction.splits, transaction.amount))}
              >
                <SplitIcon size={14} aria-hidden="true" /> {transaction.splits.length} categories
              </button>
            ) : (
              <CategoryPicker
                value={form.categoryId}
                categories={categories}
                frequentIds={frequentCategoryIds}
                onChange={(id) => patch({ categoryId: id })}
                trigger={
                  <button type="button" className="select__trigger">
                    {category?.name ?? 'Uncategorized'}
                  </button>
                }
              />
            )}
            {/* Beside the field it changes, so the button, the spinner and the
                answer are in one place. */}
            {transaction && onSuggestCategory ? (
              <Tooltip label="Ask the assistant what this should be filed under">
                <Button
                  type="button"
                  variant="secondary"
                  size="sm"
                  disabled={suggestingCategory}
                  onClick={() => onSuggestCategory(transaction)}
                >
                  {suggestingCategory ? (
                    <>
                      <Spinner /> Suggesting…
                    </>
                  ) : (
                    <>
                      <Sparkles size={14} aria-hidden="true" /> Suggest
                    </>
                  )}
                </Button>
              </Tooltip>
            ) : null}
          </div>
        </Field>

        <Field label="Tags">
          <TagPicker
            value={form.tagIds}
            tags={tags}
            onChange={(ids) => patch({ tagIds: ids })}
            trigger={
              <button type="button" className="select__trigger">
                {form.tagIds
                  .map((id) => tags.find((tag) => tag.id === id)?.name ?? 'Unknown')
                  .join(', ') || 'Tags'}
              </button>
            }
          />
        </Field>

        {transaction ? (
          <div className="txn-form__full">
            {drafts !== null ? (
              <>
                <SplitEditor
                  parent={splitParent}
                  drafts={drafts}
                  categories={categories}
                  frequentCategoryIds={frequentCategoryIds}
                  saving={saving}
                  explain
                  onChange={setDrafts}
                />
                <p className="hint" role="status">
                  {splitBlock === null
                    ? 'Update saves these splits with the rest of the transaction.'
                    : `Update is unavailable until the splits add up. ${splitBlock}`}
                </p>
                <Button variant="ghost" size="sm" onClick={() => setDrafts(null)}>
                  Discard split changes
                </Button>
              </>
            ) : (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setDrafts(initialDrafts(transaction.splits, transaction.amount))}
              >
                <SplitIcon size={14} />
                {hasSplits
                  ? `Edit ${transaction.splits.length} splits`
                  : 'Split across several categories'}
              </Button>
            )}
          </div>
        ) : null}

        {transaction ? <RefundLinksPanel transactionId={transaction.id} /> : null}
        {transaction ? (
          <MerchantPanel transaction={transaction} onPick={() => onMerchantOrder(transaction)} />
        ) : null}
        {/* Only on a saved row: an attachment needs a transaction id to hang
            off, and the create form does not have one yet. */}
        {transaction ? (
          <AttachmentPanel
            transactionId={transaction.id}
            receiptStatus={transaction.receipt_status}
          />
        ) : null}
        {/* Only where the account asks for one: a fee or an adjustment has no
            paperwork, and saying so is the honest way off the count. */}
        {transaction?.receipt_status === 'missing' || transaction?.receipt_status === 'not_needed' ? (
          <div className="txn-exclusions__item txn-form__full">
            <Checkbox
              checked={form.receiptNotNeeded}
              onCheckedChange={(checked) => patch({ receiptNotNeeded: checked === true })}
              label="No receipt needed"
            />
            <span className="txn-exclusions__consequence">
              Takes this row off the missing receipts count.
            </span>
          </div>
        ) : null}

        <Field label="Note" className="txn-form__full">
          <Textarea
            value={form.notes}
            placeholder="Add your note here"
            onChange={(event) => patch({ notes: event.target.value })}
          />
        </Field>

        {form.flagged ? (
          <Field label="Flag note" className="txn-form__full" hint="Why this one is flagged.">
            <Input
              value={form.flagNote}
              placeholder="Check this against the receipt"
              onChange={(event) => patch({ flagNote: event.target.value })}
            />
          </Field>
        ) : null}

        {transaction ? <GoalPanel transaction={transaction} /> : null}
        <Field label="Check #">
          <Input
            numeric
            value={form.checkNumber}
            onChange={(event) => patch({ checkNumber: event.target.value })}
          />
        </Field>
        <div className="txn-exclusions txn-form__full">
          <p className="filter-panel__section-title">Exclude from</p>
          {/* Two questions, two flags. Each states the calculation it changes,
              because a checkbox whose effect is a mystery gets toggled to find out. */}
          <div className="txn-exclusions__item">
            <Checkbox
              checked={form.excludedFromSpendingPlan}
              onCheckedChange={(checked) => patch({ excludedFromSpendingPlan: checked === true })}
              label={
                <span className="row row--wrap txn-actions">
                  <PiggyBank size={13} /> Spending Plan
                </span>
              }
            />
            <span className="txn-exclusions__consequence">Affects Spending Plan calculations.</span>
          </div>
          <div className="txn-exclusions__item">
            <Checkbox
              checked={form.excludedFromReports}
              onCheckedChange={(checked) => patch({ excludedFromReports: checked === true })}
              label={
                <span className="row row--wrap txn-actions">
                  <Eye size={13} /> Reports
                </span>
              }
            />
            <span className="txn-exclusions__consequence">
              Affects Reports, Watchlists and the Upcoming Summary.
            </span>
          </div>
        </div>

      </div>

      <div className="dialog__footer">
        <DialogActions
          onCancel={() => onOpenChange(false)}
          start={
            transaction ? (
              <Button variant="danger" onClick={() => onDelete(transaction.id)}>
                Delete transaction
              </Button>
            ) : null
          }
        >
          <Button type="submit" variant="primary" disabled={saving || splitBlock !== null}>
            {transaction ? 'Update' : 'Create'}
          </Button>
        </DialogActions>
      </div>
    </>
  )
}

/**
 * The pending suggestion, above the form it decides: what the proposal would
 * do, the model's reason, and the run one click away. A proposed category is changed
 * from beside Approve, so a phone reaches it without scrolling to the form.
 */
function SuggestionBanner({
  review,
  categories,
  frequentCategoryIds,
}: {
  review: ReviewFlow
  categories: readonly Category[]
  frequentCategoryIds: readonly Uuid[]
}) {
  const { suggestion } = review
  const moneyText = useMoneyText()
  const runId = suggestion.run_id
  const proposed = suggestedCategoryId(suggestion)
  // Per-split category overrides, keyed by position. The user changes one by
  // tapping the category on any split line; the rest stay as proposed.
  const [splitEdits, setSplitEdits] = useState<Record<number, Uuid | null>>({})
  const editSplitCategory = (index: number, categoryId: Uuid | null) =>
    setSplitEdits((current) => ({ ...current, [index]: categoryId }))
  const hasSplitEdits = suggestion.splits.some(
    (split, i) => i in splitEdits && splitEdits[i] !== split.category_id,
  )
  const buildOverrides = () => {
    const overrides: { index: number; category_id: string }[] = []
    for (const [index, categoryId] of Object.entries(splitEdits)) {
      const i = Number(index)
      if (suggestion.splits[i]?.category_id !== categoryId) {
        overrides.push({ index: i, category_id: categoryId ?? '' })
      }
    }
    return overrides.length > 0 ? overrides : undefined
  }
  // Choosing the proposal itself is approving it, and choosing no category is
  // discarding it: neither is a disagreement worth recording as a correction.
  const chooseCategory = (categoryId: Uuid | null) => {
    if (categoryId === proposed) review.onApprove()
    else if (categoryId === null) review.onDiscard()
    else review.onUseMine(categoryId)
  }

  return (
    <div className="txn-review">
      <p className="txn-review__head">
        <Sparkles size={14} aria-hidden="true" />{' '}
        <span>{describeSuggestion(suggestion, categories)}</span>
      </p>
      {suggestion.splits.length > 0 ? (
        <ul className="txn-review__splits">
          {suggestion.splits.map((split, index) => {
            const currentCat = index in splitEdits ? splitEdits[index] : split.category_id
            return (
              <li key={`${index}-${split.memo}`}>
                <span className="txn-review__amount">{moneyText(split.amount)}</span>
                <CategoryPicker
                  value={currentCat}
                  categories={categories}
                  frequentIds={[]}
                  trigger={
                    <button type="button" className="txn-review__category txn-review__category--editable">
                      {categoryLabel(categories, currentCat)}
                      <ChevronDown size={12} aria-hidden="true" />
                    </button>
                  }
                  onChange={(id) => editSplitCategory(index, id)}
                />
                {split.memo ? (
                  <ul className="txn-review__items">
                    {split.memo.split(', ').map((item, i) => (
                      <li key={i}>{item}</li>
                    ))}
                  </ul>
                ) : null}
              </li>
            )
          })}
        </ul>
      ) : null}
      {suggestion.summary ? <p className="txn-review__why">{suggestion.summary}</p> : null}

      <div className="txn-review__actions">
        <Button variant="primary" size="sm" disabled={review.busy} onClick={() => review.onApprove(buildOverrides())}>
          <Check size={14} /> {hasSplitEdits ? 'Approve with changes' : 'Approve'}
        </Button>
        {proposed === undefined ? null : (
          <CategoryPicker
            value={proposed}
            categories={categories}
            frequentIds={frequentCategoryIds}
            onChange={chooseCategory}
            trigger={
              <Button variant="secondary" size="sm" disabled={review.busy}>
                <Pencil size={14} /> Change category
              </Button>
            }
          />
        )}
        <Button variant="ghost" size="sm" disabled={review.busy} onClick={review.onDiscard}>
          <X size={14} /> Discard suggestion
        </Button>
        {runId === null ? null : (
          <Button variant="ghost" size="sm" onClick={() => review.onShowRun(runId)}>
            Why? See the run
          </Button>
        )}
      </div>
    </div>
  )
}

/** What to say under the amount. The exchange rate appears here and nowhere else. */
function amountHint(
  hasSplits: boolean,
  transaction: Transaction | null,
  moneyText: MoneyFormatter,
): string | undefined {
  if (hasSplits) return 'Re-split the transaction to change its amount.'
  if (!transaction || transaction.amount_primary === null) return undefined
  const rate = transaction.fx_rate_used
  const converted = moneyText(transaction.amount_primary)
  return rate === null
    ? `Counted as ${converted}.`
    : `Counted as ${converted}, at ${Number(rate).toFixed(4)} on the day.`
}

/**
 * The pair a padding mail rule writes: a purchase paid out of pay, and the pay
 * recorded as income beside it. The register lists the purchase and folds the
 * income away, so each side names the other here.
 */
function PaddingNote({
  transaction,
  onOpenRow,
}: {
  transaction: Transaction
  onOpenRow?: (id: Uuid) => void
}) {
  const other = transaction.padding_txn_id ?? transaction.padded_txn_id
  if (other === null) return null
  const open = onOpenRow ? (
    <>
      {' '}
      <button type="button" className="link" onClick={() => onOpenRow(other)}>
        {transaction.padding_txn_id ? 'Open the income row' : 'Open the purchase'}
      </button>
    </>
  ) : null
  return (
    <>
      <br />
      {transaction.padding_txn_id
        ? 'Paid by payroll deduction: the pay it came out of is recorded as income beside it, hidden from the register.'
        : 'Pay taken for a purchase by payroll deduction, recorded as income. The register hides it; every balance and figure counts it.'}
      {open}
    </>
  )
}
