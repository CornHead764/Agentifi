import {
  CircleCheck,
  Eye,
  Link2,
  Link2Off,
  Package,
  Pencil,
  PiggyBank,
  Receipt,
  RefreshCw,
  Repeat,
  RotateCcw,
  Sparkles,
  Tag,
  Tags as TagsIcon,
  Trash2,
  Wand2,
} from 'lucide-react'

import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { OverflowMenu } from '@/components/ui'
import { transactionSubject } from '@/lib/assistant/subjects'
import { merchantsFor } from '@/lib/merchants'
import type { Transaction } from '@/lib/transactions/types'

/**
 * The per-row overflow. *Create a rule* and *Create a series* open seeded
 * editors rather than saving on the spot; linking acts directly, and the
 * server refuses a slot already paid.
 *
 * On a phone, `open`/`onOpenChange` and `anchorOnly` let a swipe open the
 * menu.
 */
export function RowMenu({
  txn,
  open,
  onOpenChange,
  anchorOnly = false,
  anchorPoint,
  onEdit,
  onReview,
  onDelete,
  onToggleExclusion,
  onSetReviewed,
  onCreateRule,
  onCreateSeries,
  onLinkSeries,
  onUnlinkSeries,
  onToggleBill,
  onToggleSubscription,
  onLinkRefund,
  canBeARefund,
  onMerchantOrder,
  onSuggestCategory,
  onEditTags,
  tagTargetCount = 1,
}: {
  txn: Transaction
  onEdit: (txn: Transaction) => void
  /** Open the row in the review flow: the same form, with whatever the
   *  assistant proposed above it and the next unreviewed row after it. */
  onReview: (txn: Transaction) => void
  onDelete: (txn: Transaction) => void
  onToggleExclusion: (txn: Transaction, field: 'reports' | 'spending_plan', next: boolean) => void
  onSetReviewed: (txn: Transaction, reviewed: boolean) => void
  onCreateRule: (txn: Transaction) => void
  onCreateSeries: (txn: Transaction) => void
  onLinkSeries: (txn: Transaction) => void
  onUnlinkSeries: (txn: Transaction) => void
  onToggleBill: (txn: Transaction, next: boolean) => void
  onToggleSubscription: (txn: Transaction, next: boolean) => void
  /** The purchase behind the row, offered when its wording names a merchant. */
  /**
   * Offered only on a credit a refund could apply to (`canBeARefund`; the
   * server checks again). Not a charge, income, or a transfer leg.
   */
  onLinkRefund?: (txn: Transaction) => void
  canBeARefund?: boolean
  onMerchantOrder?: (txn: Transaction) => void
  onSuggestCategory?: (txn: Transaction) => void
  /** Add or remove tags on the rows this menu acts on: the selection, or the row alone. */
  onEditTags?: (txn: Transaction, mode: 'add' | 'remove') => void
  /** How many rows the tag actions reach, so the label can say "3 selected rows". */
  tagTargetCount?: number
  open?: boolean
  onOpenChange?: (open: boolean) => void
  anchorOnly?: boolean
  /** Where a right-click landed: the menu opens there instead of by the button. */
  anchorPoint?: { x: number; y: number }
}) {
  const selectedRows = tagTargetCount > 1 ? ` ${tagTargetCount} selected rows` : ''
  return (
    <OverflowMenu
      label={`Actions for ${txn.payee || txn.statement_name}`}
      open={open}
      onOpenChange={onOpenChange}
      anchorOnly={anchorOnly}
      anchorPoint={anchorPoint}
      actions={[
        { label: 'Edit transaction…', icon: <Pencil size={14} />, onSelect: () => onEdit(txn) },
        // One sparkle: a row with a suggestion opens it, a row without asks for one.
        txn.suggestion || !onSuggestCategory
          ? {
              label: txn.suggestion ? 'Review the suggestion…' : 'Review this row…',
              icon: <Sparkles size={14} />,
              onSelect: () => onReview(txn),
            }
          : { label: 'Suggest a category', icon: <Sparkles size={14} />, onSelect: () => onSuggestCategory(txn) },
        txn.suggestion && onSuggestCategory
          ? { label: 'Suggest a category again', icon: <RefreshCw size={14} />, onSelect: () => onSuggestCategory(txn) }
          : null,
        // Still here, and still one click. Reviewing is looking at the row and
        // saying yes; this is for the rows somebody has already looked at.
        {
          label: txn.is_reviewed ? 'Mark as unreviewed' : 'Mark as reviewed',
          icon: <CircleCheck size={14} />,
          onSelect: () => onSetReviewed(txn, !txn.is_reviewed),
        },
        onEditTags
          ? { label: selectedRows === '' ? 'Add tags…' : `Add tags to${selectedRows}…`, icon: <Tag size={14} />, onSelect: () => onEditTags(txn, 'add') }
          : null,
        onEditTags
          ? { label: selectedRows === '' ? 'Remove tags…' : `Remove tags from${selectedRows}…`, icon: <TagsIcon size={14} />, onSelect: () => onEditTags(txn, 'remove') }
          : null,
        <AskMenuItem subject={() => transactionSubject(txn)} />,
        { label: 'Delete transaction', icon: <Trash2 size={14} />, danger: true, onSelect: () => onDelete(txn) },
      ]}
      sections={[
        {
          entries: [
            { label: 'Create a rule…', icon: <Wand2 size={14} />, onSelect: () => onCreateRule(txn) },
            onMerchantOrder && merchantsFor(txn).length > 0
              ? { label: 'Purchase behind this row…', icon: <Package size={14} />, onSelect: () => onMerchantOrder(txn) }
              : null,
          ],
        },
        {
          label: 'Series',
          entries: [
            {
              label: txn.is_bill ? 'Unmark as one-time bill' : 'Mark as one-time bill',
              icon: <Receipt size={14} />,
              onSelect: () => onToggleBill(txn, !txn.is_bill),
            },
            {
              label: txn.is_subscription ? 'Unmark as a subscription' : 'Mark as a subscription',
              icon: <RefreshCw size={14} />,
              onSelect: () => onToggleSubscription(txn, !txn.is_subscription),
            },
            { label: 'Make recurring…', icon: <Repeat size={14} />, onSelect: () => onCreateSeries(txn) },
            txn.series_id === null
              ? { label: 'Link to a recurring item…', icon: <Link2 size={14} />, onSelect: () => onLinkSeries(txn) }
              : { label: 'Unlink from recurring item', icon: <Link2Off size={14} />, onSelect: () => onUnlinkSeries(txn) },
          ],
        },
        {
          entries: [
            canBeARefund === true && onLinkRefund !== undefined
              ? { label: 'This refunds…', icon: <RotateCcw size={14} />, onSelect: () => onLinkRefund(txn) }
              : null,
          ],
        },
        {
          label: 'Exclude from',
          entries: [
            {
              label: 'Spending Plan',
              icon: <PiggyBank size={14} />,
              checked: txn.excluded_from_spending_plan,
              onCheckedChange: (checked) => onToggleExclusion(txn, 'spending_plan', checked),
            },
            {
              label: 'Reports',
              icon: <Eye size={14} />,
              checked: txn.excluded_from_reports,
              onCheckedChange: (checked) => onToggleExclusion(txn, 'reports', checked),
            },
          ],
        },
      ]}
    />
  )
}
