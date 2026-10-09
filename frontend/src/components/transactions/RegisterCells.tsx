import {
  ArrowLeftRight,
  Check,
  CircleCheck,
  CircleHelp,
  EyeOff,
  Flag,
  HandCoins,
  PiggyBank,
  Repeat,
  Sparkles,
  Split,
} from 'lucide-react'
import type { ReactNode } from 'react'

import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { amountToWire, parseAmountInput } from '@/lib/money'
import { AttachmentIndicator, MissingReceiptMark } from '@/components/transactions/AttachmentPanel'
import { ForeignAmount } from '@/components/transactions/ForeignAmount'
import { Badge, IconButton, Spinner, Tooltip } from '@/components/ui'
import { usePrivacy } from '@/contexts/privacy'
import { formatDate } from '@/lib/format'
import type { ColumnDef } from '@/lib/transactions/columns'
import { STATEMENT_NAME_REASON, displayPayee } from '@/lib/transactions/edits'
import { categoryWhyRunId, isUndetermined } from '@/lib/transactions/categoryCheck'
import { suggestedCategoryId } from '@/lib/transactions/suggestions'
import { partialSplit, type PartialSplit } from '@/lib/transactions/partialSplit'
import type { Transaction } from '@/lib/transactions/types'
import { reviewIconAction } from '@/lib/transactions/reviewMode'

import { DateCell, LockedCell, TextCell } from './EditableCell'
import { titleWhenClipped } from './overflowTitle'
import { AccountPicker, CategoryPicker, TagPicker } from './Pickers'
import { useRegisterView, type RegisterView } from './register-context'
import { EXCLUDED_FROM_REPORTS, EXCLUDED_FROM_SPENDING_PLAN } from '@/lib/exclusions'

/**
 * One row of the register on a phone, where there are no cells: the payee
 * over the category on the left, the amount over its date on the right. The
 * payee is the one field that clips. The whole line is one button onto the
 * detail dialog, the phone's edit surface; inline editing stays on the
 * desktop. The glyphs after the payee stand in for the desktop's icon columns.
 */
export function TwoLineRow({ txn }: { txn: Transaction }) {
  const { lookups, actions } = useRegisterView()
  // The row is one button, so the "why" cannot nest inside it: it is a sibling
  // pinned to the right edge.
  const whyRun = categoryWhyRunId(txn)
  const partial = partialSplit(txn)

  return (
    <div className={whyRun === null ? 'txn-line-wrap' : 'txn-line-wrap txn-line-wrap--why'}>
      <button
        type="button"
        className="txn-line"
        onClick={() => actions.openDetail(txn)}
      >
      <span className="txn-line__title">
        <span className="txn-line__payee" onMouseEnter={titleWhenClipped}>
          {displayPayee(txn)}
        </span>
        <RowGlyphs txn={txn} />
      </span>
      <span className="txn-line__amount">
        {partial ? <PartialAmount partial={partial} /> : <ForeignAmount txn={txn} showPlus />}
      </span>
      <span className="txn-line__meta">
          {/* A suggestion is the category itself, highlighted. A tap opens the
              row in review, where the suggestion is approved or changed. */}
        {lookups.checkingCategories?.has(txn.id) ? (
          <span className="txn-line__category txn-line__category--checking">
            <Spinner size={11} />
            Suggesting…
          </span>
        ) : isUndetermined(txn) ? (
          <span
            className="txn-line__category txn-line__category--undetermined"
            title={txn.category_check_note}
          >
            Undetermined
          </span>
        ) : txn.suggestion ? (
          <Badge tone="warning" className="txn-line__category">
            <Sparkles size={11} aria-hidden="true" />
            {suggestedCategoryId(txn.suggestion) === undefined
              ? 'Suggested split'
              : lookups.categoryName(suggestedCategoryId(txn.suggestion) ?? null)}
          </Badge>
        ) : partial ? (
          <span className="txn-line__category txn-line__category--partial">
            <Split size={11} aria-hidden="true" />
            {partialCategories(partial, lookups.categoryName)}
          </span>
        ) : txn.splits.length > 0 && txn.category_id === null ? (
          <span className="txn-line__category txn-line__category--split">
            <Split size={11} aria-hidden="true" />
            {txn.splits.length} categories
          </span>
        ) : (
          <span className="txn-line__category">{categoryLabel(txn, lookups.categoryName)}</span>
        )}
          {/* No Pending badge: the register already groups pending rows under a
              "Pending" heading (see rows.ts). */}
        {txn.attachment_count > 0 ? (
          <AttachmentIndicator count={txn.attachment_count} />
        ) : txn.receipt_status === 'missing' ? (
          <MissingReceiptMark />
        ) : null}
      </span>
      <span className="txn-line__when">{formatDate(txn.date)}</span>
      </button>
      {whyRun === null ? null : (
        <button
          type="button"
          className="txn-line__why"
          aria-label="Why this category?"
          onClick={() => actions.showRun(whyRun)}
        >
          <CircleHelp size={13} aria-hidden="true" />
        </button>
      )}
    </div>
  )
}

/** Each part of a split transaction: its category, memo and amount. */
export function SplitParts({ txn }: { txn: Transaction }) {
  const { lookups } = useRegisterView()
  return (
    <ul className="split-tip">
      {txn.splits.map((split) => (
        <li key={split.id} className="split-tip__part">
          <span className="split-tip__category">
            {lookups.categoryName(split.category_id)}
            {split.memo ? <span className="split-tip__memo">{split.memo}</span> : null}
          </span>
          <Money value={split.amount} tone="flow" showPlus />
        </li>
      ))}
    </ul>
  )
}

/**
 * The parts of a split transaction, in a hint over its trigger. It opens on
 * hover and on keyboard focus, and Escape closes it.
 */
function SplitBreakdown({ txn, children }: { txn: Transaction; children: ReactNode }) {
  return (
    <Tooltip side="bottom" label={<SplitParts txn={txn} />}>
      {children}
    </Tooltip>
  )
}

/**
 * A split row a filter kept only part of: what it counts for here, over the
 * whole transaction. The glyph and the "of" line say it is partial, so the
 * row reads the same without colour.
 */
function PartialAmount({ partial }: { partial: PartialSplit }) {
  return (
    <span className="partial-amount">
      <span className="partial-amount__kept">
        <Split size={11} aria-hidden="true" />
        <span className="visually-hidden">Matching part of a split: </span>
        <Money value={partial.amount} tone="neutral" showPlus />
      </span>
      <span className="partial-amount__whole">
        of <Money value={partial.whole} tone="neutral" showPlus />
      </span>
    </span>
  )
}

/**
 * Pairing files a leg under Transfer or Credit Card Payment, but a leg a person
 * cleared keeps no category; it is still a transfer (`PartNeedsCategory`), so
 * it reads as one, not as uncategorized.
 */
function categoryLabel(
  txn: Transaction,
  categoryName: RegisterView['lookups']['categoryName'],
): string {
  if (txn.category_id === null && txn.transfer_pair_id !== null) return 'Transfer'
  return categoryName(txn.category_id)
}

function partialCategories(
  partial: PartialSplit,
  categoryName: RegisterView['lookups']['categoryName'],
): string {
  return partial.splits.map((split) => categoryName(split.category_id)).join(', ')
}

const PADDED = 'Paid by payroll deduction; the income it came out of is recorded beside it'

/**
 * The facts the desktop gives a column each, in a fixed order. They never
 * clip or wrap; the payee gives way instead.
 */
function RowGlyphs({ txn }: { txn: Transaction }) {
  const glyphs: ReactNode[] = []
  if (txn.series_id !== null) {
    glyphs.push(
      <Glyph key="series" label="Recurring item">
        <Repeat size={11} aria-hidden="true" />
      </Glyph>,
    )
  }
  if (txn.transfer_pair_id !== null) {
    glyphs.push(
      <Glyph key="transfer" label="One leg of a transfer between your own accounts">
        <ArrowLeftRight size={11} aria-hidden="true" />
      </Glyph>,
    )
  }
  if (txn.padding_txn_id !== null) {
    glyphs.push(
      <Glyph key="padded" label={PADDED}>
        <HandCoins size={11} aria-hidden="true" />
      </Glyph>,
    )
  }
  if (txn.splits.length > 0) {
    glyphs.push(
      <Glyph key="split" label={`Split ${txn.splits.length} ways`}>
        <Split size={11} aria-hidden="true" />
      </Glyph>,
    )
  }
  if (txn.excluded_from_reports) {
    glyphs.push(
      <Glyph key="excluded" label="Excluded from reports">
        <EyeOff size={11} aria-hidden="true" />
      </Glyph>,
    )
  }
  if (txn.user_flag !== null) {
    glyphs.push(
      <Glyph key="flag" label={txn.user_flag_note || 'Flagged'}>
        <Flag size={11} fill="currentColor" aria-hidden="true" />
      </Glyph>,
    )
  }

  if (glyphs.length === 0) return null
  return <span className="txn-line__glyphs">{glyphs}</span>
}

/**
 * `role="img"` with the label on the wrapper, not on the svg: the row is a
 * button, and a bare labelled svg inside one is read as part of the button's
 * own name on some screen readers and skipped entirely on others.
 */
function Glyph({ label, children }: { label: string; children: ReactNode }) {
  return (
    <span className="txn-line__glyph" role="img" aria-label={label} title={label}>
      {children}
    </span>
  )
}

/**
 * The "why?" beside a category the assistant decided: opens the run that
 * decided it. A row with no run shows nothing.
 */
function WhyCategory({ txn }: { txn: Transaction }) {
  const { actions } = useRegisterView()
  const runId = categoryWhyRunId(txn)
  if (runId === null) return null
  return (
    <Tooltip label="Why this category?">
      <IconButton
        size="sm"
        variant="ghost"
        className="txn-mark"
        label="Why this category?"
        onClick={() => actions.showRun(runId)}
      >
        <CircleHelp size={13} aria-hidden="true" />
      </IconButton>
    </Tooltip>
  )
}

/**
 * The category cell of a row the assistant has proposed something for. The
 * proposed category is shown, marked as a proposal. The tick applies it and
 * ticks the row reviewed; choosing another category in the picker applies
 * that instead, recording the disagreement as a correction. A proposed split
 * has no single category, so the cell names the parts.
 */
function SuggestedCategory({
  txn,
  suggestion,
}: {
  txn: Transaction
  suggestion: NonNullable<Transaction['suggestion']>
}) {
  const { lookups, actions } = useRegisterView()
  const moneyText = useMoneyText()
  const proposed = suggestedCategoryId(suggestion)
  const busy = actions.decidingSuggestion

  if (proposed === undefined) {
    // A split can still be approved from here; the parts are named on the tick
    // so nobody approves a thing unseen.
    const parts = suggestion.splits
      .map((split) => `${lookups.categoryName(split.category_id)} ${moneyText(split.amount)}`)
      .join(' · ')
    return (
      <span className="cell-suggested">
        <Tooltip label={suggestion.summary || 'A change is waiting on this row'}>
          <button
            type="button"
            className="cell-edit cell-suggested__open"
            onClick={() => actions.openDetail(txn)}
            onMouseEnter={titleWhenClipped}
          >
            <Sparkles size={11} aria-hidden="true" />{' '}
            {suggestion.splits.length > 0
              ? `${suggestion.splits.length} categories`
              : 'Suggested split'}
          </button>
        </Tooltip>
        {suggestion.splits.length > 0 ? (
          <Tooltip label={`Approve the split: ${parts}`}>
            <IconButton
              size="sm"
              variant="ghost"
              className="txn-mark txn-mark--approve"
              disabled={busy}
              label={`Approve the split: ${parts}`}
              onClick={() => actions.applySuggestion(txn)}
            >
              <Check size={13} />
            </IconButton>
          </Tooltip>
        ) : null}
      </span>
    )
  }

  const label = lookups.categoryName(proposed)
  return (
    <span className="cell-suggested">
      <CategoryPicker
        value={proposed}
        categories={lookups.categories}
        frequentIds={lookups.frequentCategoryIds}
        onChange={(next) => actions.applySuggestion(txn, next)}
        trigger={
          <button
            type="button"
            className="cell-edit cell-suggested__pick"
            disabled={busy}
            aria-label={`Suggested category: ${label}. Choose a different one`}
            onMouseEnter={titleWhenClipped}
          >
            <Sparkles size={11} aria-hidden="true" /> {label}
          </button>
        }
      />
      <Tooltip label={suggestion.summary || `File this under ${label}`}>
        <IconButton
          size="sm"
          variant="ghost"
          className="txn-mark txn-mark--approve"
          disabled={busy}
          label={`Approve: file this under ${label}`}
          onClick={() => actions.applySuggestion(txn)}
        >
          <Check size={13} />
        </IconButton>
      </Tooltip>
    </span>
  )
}

/**
 * One cell of one row. Every edit goes through `actions.edit` with the request
 * body and the optimistic row separately: the wire carries an amount as a
 * string and the cache holds it as `Money`.
 */
export function RegisterCell({ column, txn }: { column: ColumnDef; txn: Transaction }) {
  const { lookups, actions, multiAccount } = useRegisterView()
  const { hidden } = usePrivacy()
  const partial = partialSplit(txn)

  switch (column.id) {
    case 'date':
      return (
        <DateCell
          value={txn.date}
          label="Date"
          display={formatDate(txn.date)}
          onCommit={(next) => actions.edit(txn, { date: next }, { date: next })}
        />
      )

    case 'account':
      return (
        <AccountPicker
          value={txn.account_id}
          accounts={lookups.accounts}
          onChange={(next) => actions.edit(txn, { account_id: next }, { account_id: next })}
          trigger={
            <button
              type="button"
              className="cell-edit"
              aria-label={`Account: ${lookups.accountName(txn.account_id)}`}
              onMouseEnter={titleWhenClipped}
            >
              {lookups.accountName(txn.account_id)}
            </button>
          }
        />
      )

    case 'flag':
      return (
        <Tooltip label={txn.user_flag ? 'Remove flag' : 'Flag this transaction'}>
          <IconButton
            size="sm"
            variant="ghost"
            className="txn-mark txn-mark--flag"
            data-on={txn.user_flag !== null}
            aria-pressed={txn.user_flag !== null}
            label={txn.user_flag ? 'Remove flag' : 'Flag this transaction'}
            onClick={() => {
              const next = txn.user_flag === null ? 'flagged' : null
              actions.edit(txn, { user_flag: next }, { user_flag: next })
            }}
          >
            <Flag size={13} fill={txn.user_flag ? 'currentColor' : 'none'} />
          </IconButton>
        </Tooltip>
      )

    // Out of review mode an unreviewed row opens, so a suggestion is never
    // thrown away unseen. In review mode the icon ticks the row in place,
    // except a row with a suggestion waiting, which still opens.
    case 'reviewed': {
      const reviewMode = actions.reviewMode === true
      const label = txn.is_reviewed
        ? 'Mark as unreviewed'
        : reviewMode && !txn.suggestion
          ? 'Mark as reviewed'
          : 'Review this transaction'
      return (
        <Tooltip label={label}>
          <IconButton
            size="sm"
            variant="ghost"
            className="txn-mark"
            data-on={txn.is_reviewed}
            data-waiting={Boolean(txn.suggestion)}
            aria-pressed={txn.is_reviewed}
            label={label}
            onClick={() => {
              if (reviewIconAction(txn, reviewMode) === 'open') actions.openDetail(txn)
              else actions.setReviewed(txn, !txn.is_reviewed)
            }}
          >
            <CircleCheck size={14} />
          </IconButton>
        </Tooltip>
      )
    }

    case 'payee': {
      const payee = (
        <TextCell
          value={txn.payee}
          label="Payee"
          placeholder={txn.statement_name}
          onCommit={(next) => actions.edit(txn, { payee: next }, { payee: next })}
        />
      )
      return txn.padding_txn_id === null ? (
        payee
      ) : (
        <span className="cell-marked">
          {payee}
          <Tooltip label={PADDED}>
            <HandCoins size={13} aria-label={PADDED} className="cell-marked__mark" />
          </Tooltip>
        </span>
      )
    }

    // Ground rule 5: the bank's string, shown and not editable.
    case 'statement_name':
      return (
        <LockedCell
          value={txn.statement_name}
          label="Statement name"
          reason={STATEMENT_NAME_REASON}
          onRefuse={actions.refuse}
        />
      )

    case 'category':
      if (lookups.checkingCategories?.has(txn.id)) {
        return (
          <span className="cell-edit cell-checking">
            <Spinner size={13} />
            Suggesting…
          </span>
        )
      }
      if (partial && !txn.suggestion) {
        return (
          <SplitBreakdown txn={txn}>
            <button
              type="button"
              className="cell-edit cell-edit--split cell-edit--partial"
              aria-label={`${partialCategories(partial, lookups.categoryName)}, part of a ${
                txn.splits.length
              }-way split`}
              onClick={() => actions.openDetail(txn)}
              onMouseEnter={titleWhenClipped}
            >
              <Split size={11} aria-hidden="true" />
              {partialCategories(partial, lookups.categoryName)}
            </button>
          </SplitBreakdown>
        )
      }
      if (txn.splits.length > 0 && txn.category_id === null && !txn.suggestion) {
        return (
          <SplitBreakdown txn={txn}>
            <button
              type="button"
              className="cell-edit cell-edit--split"
              onClick={() => actions.openDetail(txn)}
            >
              <Split size={11} aria-hidden="true" />
              {txn.splits.length} categories
            </button>
          </SplitBreakdown>
        )
      }
      if (txn.suggestion) {
        return <SuggestedCategory txn={txn} suggestion={txn.suggestion} />
      }
      // Undetermined still opens the picker: it reports on the check, it is not
      // a lock. The model's reason rides on the title.
      {
        const picker = (
          <CategoryPicker
            value={txn.category_id}
            categories={lookups.categories}
            frequentIds={lookups.frequentCategoryIds}
            onChange={(next) =>
              actions.edit(
                txn,
                { category_id: next, is_reviewed: true },
                { category_id: next, is_reviewed: true },
              )
            }
            trigger={
              isUndetermined(txn) ? (
                <button
                  type="button"
                  className="cell-edit cell-edit--undetermined"
                  aria-label={`Category: undetermined. ${txn.category_check_note}`}
                  title={txn.category_check_note}
                >
                  Undetermined
                </button>
              ) : (
                <button
                  type="button"
                  className={
                    txn.category_id === null && txn.transfer_pair_id === null
                      ? 'cell-edit cell-edit--empty'
                      : 'cell-edit'
                  }
                  aria-label={`Category: ${categoryLabel(txn, lookups.categoryName)}`}
                  onMouseEnter={titleWhenClipped}
                >
                  {categoryLabel(txn, lookups.categoryName)}
                </button>
              )
            }
          />
        )
        // The flex wrapper reshapes the cell-edit bleed, so only a row with a
        // run to show gets it.
        return categoryWhyRunId(txn) === null ? (
          picker
        ) : (
          <span className="cell-why">
            {picker}
            <WhyCategory txn={txn} />
          </span>
        )
      }

    case 'split':
      return txn.splits.length > 0 ? (
        <SplitBreakdown txn={txn}>
          <IconButton
            size="sm"
            variant="ghost"
            className="txn-mark txn-mark--muted"
            data-on={true}
            label={`${txn.splits.length} splits`}
            onClick={() => actions.openDetail(txn)}
          >
            <Split size={13} />
          </IconButton>
        </SplitBreakdown>
      ) : null

    case 'tags':
      return (
        <TagPicker
          value={txn.tag_ids}
          tags={lookups.tags}
          onChange={(next) => actions.setTags(txn, next)}
          trigger={
            <button
              type="button"
              className={txn.tag_ids.length === 0 ? 'cell-edit cell-edit--empty' : 'cell-edit'}
              aria-label={`Tags: ${txn.tag_ids.map(lookups.tagName).join(', ') || 'none'}`}
              onMouseEnter={titleWhenClipped}
            >
              {txn.tag_ids.map(lookups.tagName).join(', ') || '—'}
            </button>
          }
        />
      )

    case 'notes':
      return (
        <TextCell
          value={txn.notes ?? ''}
          label="Notes"
          onCommit={(next) =>
            actions.edit(txn, { notes: next || null }, { notes: next || null })
          }
        />
      )

    // A count rather than a flag, so the tooltip can say how many. Open the
    // row to see them: the register has no room for a thumbnail. A row owing
    // a receipt opens on the panel that takes one.
    case 'attachment':
      return txn.receipt_status === 'missing' ? (
        <MissingReceiptMark onOpen={() => actions.openDetail(txn)} />
      ) : (
        <AttachmentIndicator count={txn.attachment_count} />
      )

    // Two flags, two icons, independently. Each says which calculation it changes.
    case 'exclusion':
      return (
        <span className="exclusion-cell">
          {txn.excluded_from_spending_plan ? (
            <Tooltip label={EXCLUDED_FROM_SPENDING_PLAN.state}>
              <PiggyBank size={13} aria-label={EXCLUDED_FROM_SPENDING_PLAN.state} />
            </Tooltip>
          ) : null}
          {txn.excluded_from_reports ? (
            <Tooltip label={EXCLUDED_FROM_REPORTS.state}>
              <EyeOff size={13} aria-label={EXCLUDED_FROM_REPORTS.state} />
            </Tooltip>
          ) : null}
        </span>
      )

    case 'transfer':
      return txn.transfer_pair_id === null ? null : (
        <Tooltip label="One leg of a transfer between your own accounts">
          <ArrowLeftRight size={13} aria-label="One leg of a transfer between your own accounts" />
        </Tooltip>
      )

    case 'amount':
      return (
        <TextCell
          value={amountToWire(txn.amount)}
          label="Amount"
          numeric
          display={
            partial ? <PartialAmount partial={partial} /> : <ForeignAmount txn={txn} showPlus />
          }
          // Editing in privacy mode would put the digits back on screen in the
          // input, which is the one thing the mode exists to prevent.
          disabled={hidden}
          onCommit={(next) => {
            const parsed = parseAmountInput(next)
            if (parsed === null) {
              actions.refuse(`"${next}" is not an amount.`)
              return
            }
            actions.edit(txn, { amount: parsed.wire }, { amount: parsed.cents })
          }}
        />
      )

    // A running balance is a per-account figure. Across accounts it is the sum
    // of unrelated ledgers, which is a number that means nothing.
    case 'balance':
      if (multiAccount || txn.balance === null) {
        return (
          <Tooltip
            label={
              multiAccount
                ? 'A running balance only means something inside one account.'
                : 'No stored balance for this row.'
            }
          >
            <span aria-label="No running balance">—</span>
          </Tooltip>
        )
      }
      return <Money value={txn.balance} tone="neutral" />

    case 'check_number':
      return (
        <TextCell
          value={txn.check_number ?? ''}
          label="Check number"
          numeric
          onCommit={(next) =>
            actions.edit(txn, { check_number: next || null }, { check_number: next || null })
          }
        />
      )
  }
}
