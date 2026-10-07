import { Plus, Scale, Trash2 } from 'lucide-react'
import { useState } from 'react'

import { Money } from '@/components/Money'
import { Button, DialogActions, IconButton, Input, Tooltip } from '@/components/ui'
import { ZERO_MONEY, type Money as MoneyValue } from '@/lib/money'
import {
  absorbRemainder,
  describeProblem,
  newSplitDraft,
  validateSplits,
  type SplitDraft,
} from '@/lib/transactions/splits'
import type { Category, SplitWrite, Uuid } from '@/lib/transactions/types'

import { CategoryPicker } from './Pickers'

/**
 * Editing a row's allocations. Save stays disabled until the parts sum to the
 * whole, and the remainder is always shown: the server refuses an unbalanced
 * set, and learning that from a rejected request loses what was typed.
 */
export function SplitEditor({
  parent,
  drafts,
  categories,
  frequentCategoryIds,
  saving,
  onChange,
  onSave,
  onCancel,
}: {
  parent: MoneyValue
  drafts: readonly SplitDraft[]
  categories: readonly Category[]
  frequentCategoryIds: readonly Uuid[]
  saving: boolean
  onChange: (drafts: SplitDraft[]) => void
  /** Omit both for a grid with no footer, inside a form that saves itself. */
  onSave?: (rows: SplitWrite[]) => void
  onCancel?: () => void
}) {
  const [touched, setTouched] = useState(false)
  const validation = validateSplits(drafts, parent)

  const update = (key: string, patch: Partial<SplitDraft>) => {
    setTouched(true)
    onChange(drafts.map((draft) => (draft.key === key ? { ...draft, ...patch } : draft)))
  }

  return (
    <div className="split-editor">
      <div className="split-editor__grid">
        {drafts.map((draft) => (
          <SplitRow
            key={draft.key}
            draft={draft}
            categories={categories}
            frequentCategoryIds={frequentCategoryIds}
            removable={drafts.length > 2}
            onChange={(patch) => update(draft.key, patch)}
            onBalance={() => {
              setTouched(true)
              onChange(absorbRemainder(drafts, draft.key, parent))
            }}
            onRemove={() => {
              setTouched(true)
              onChange(drafts.filter((row) => row.key !== draft.key))
            }}
          />
        ))}
      </div>

      <Button
        variant="ghost"
        size="sm"
        onClick={() => {
          setTouched(true)
          onChange([...drafts, newSplitDraft('')])
        }}
      >
        <Plus size={14} /> Add a part
      </Button>

      <div className="split-editor__foot">
        <span
          className="split-editor__remainder hint"
          data-unbalanced={validation.remainder !== ZERO_MONEY}
        >
          {validation.remainder === ZERO_MONEY ? (
            'Fully allocated.'
          ) : (
            <>
              <Money value={validation.remainder} tone="neutral" /> left to allocate
            </>
          )}
          {touched && validation.problems.length > 0
            ? ` ${validation.problems.map(describeProblem).join(' ')}`
            : null}
        </span>
        {onSave && onCancel ? (
          <span className="row row--wrap txn-actions">
            <DialogActions onCancel={onCancel}>
              <Button
                variant="primary"
                disabled={validation.rows === null || saving}
                onClick={() => {
                  if (validation.rows !== null) onSave(validation.rows)
                }}
              >
                Save splits
              </Button>
            </DialogActions>
          </span>
        ) : null}
      </div>
    </div>
  )
}

function SplitRow({
  draft,
  categories,
  frequentCategoryIds,
  removable,
  onChange,
  onBalance,
  onRemove,
}: {
  draft: SplitDraft
  categories: readonly Category[]
  frequentCategoryIds: readonly Uuid[]
  removable: boolean
  onChange: (patch: Partial<SplitDraft>) => void
  onBalance: () => void
  onRemove: () => void
}) {
  const category = categories.find((row) => row.id === draft.category_id)

  return (
    <>
      <Input
        value={draft.memo ?? ''}
        placeholder="Memo"
        aria-label="Split memo"
        onChange={(event) => onChange({ memo: event.target.value || null })}
      />
      <CategoryPicker
        value={draft.category_id}
        categories={categories}
        frequentIds={frequentCategoryIds}
        onChange={(id) => onChange({ category_id: id })}
        trigger={
          <button type="button" className="select__trigger" aria-label="Split category">
            {category?.name ?? 'Uncategorized'}
          </button>
        }
      />
      <Input
        numeric
        value={draft.amount}
        placeholder="0.00"
        aria-label="Split amount"
        onChange={(event) => onChange({ amount: event.target.value })}
      />
      <span className="row row--wrap txn-actions">
        <Tooltip label="Give this part the unallocated remainder">
          <IconButton
            label="Give this part the unallocated remainder"
            variant="ghost"
            size="sm"
            onClick={onBalance}
          >
            <Scale size={14} />
          </IconButton>
        </Tooltip>
        {removable ? (
          <IconButton label="Remove this part" variant="ghost" size="sm" onClick={onRemove}>
            <Trash2 size={14} />
          </IconButton>
        ) : null}
      </span>
    </>
  )
}
