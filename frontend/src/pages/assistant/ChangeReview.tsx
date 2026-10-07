import { Fragment, useMemo } from 'react'

import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { CategoryPicker } from '@/components/transactions/Pickers'
import { describeAbout, describeChange, type ChangeView } from '@/lib/assistant/change'
import { categoryLabel } from '@/lib/categoryNames'
import type { AssistantAction } from '@/lib/clients/assistant'
import { useAccountName, useCategories } from '@/lib/transactions/queries'
import type { Category, Uuid } from '@/lib/transactions/types'

/**
 * What a proposed change does, with the categories named beside each amount
 * and item, since approving a category id is approving a uuid.
 *
 * Each category is a picker. Choosing another edits neither the ledger nor the
 * proposal: it is sent with Apply, the server writes it into the request and
 * keeps the model's choice beside it, and the disagreement is recorded for the
 * next run about this payee.
 *
 * Nothing else is editable: a card whose amount could be rewritten would be a
 * change authored under the model's name rather than one approved.
 */
export function ChangeReview({
  action,
  chosen,
  onChoose,
  editable,
}: {
  action: AssistantAction
  /** The categories the person has picked, by split index; -1 is the row's own. */
  chosen: Readonly<Record<number, Uuid | null>>
  onChoose: (index: number, id: Uuid | null) => void
  editable: boolean
}) {
  const view = useMemo(() => describeChange(action), [action])
  const about = useMemo(() => describeAbout(action), [action])
  const categories = useCategories()
  const accountName = useAccountName()
  const moneyText = useMoneyText()
  const rows = categories.data ?? NO_CATEGORIES
  const frequent = useMemo(() => frequentIds(view, rows), [view, rows])

  // The order stands on its own even when the request cannot be read back:
  // "a category for what" is a fair question about any card.
  if (view.opaque && about.items.length === 0) return null


  return (
    <div className="change-review">
      {about.items.length > 0 ? (
        <div className="change-review__items">
          <span className="hint hint--faint">
            {about.items.length === 1 ? 'The item' : 'The order'}
          </span>
          <ul>
            {about.items.map((item, index) => (
              <li key={`${index}-${item.title}`}>
                {/* Neutral: a share of what the charge was for is a reference
                    figure. The split parts below are the request and stay
                    coloured. */}
                {item.amount === null ? null : (
                  <>
                    <Money value={item.amount} tone="neutral" />{' '}
                  </>
                )}
                {item.title}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {view.splits.length > 0 ? (
        <>
          <p className="change-review__kind">
            {view.kind}
            {view.total !== null ? (
              <span className="muted"> · {moneyText(view.total)} in all</span>
            ) : null}
          </p>
          <ul className="change-review__splits">
            {view.splits.map((split) => (
              <li key={split.index} className="change-review__split">
                <span className="change-review__amount">
                  {split.amount === null ? (
                    split.amountText
                  ) : (
                    <Money value={split.amount} />
                  )}
                </span>
                <CategoryField
                  categories={rows}
                  frequentIds={frequent}
                  editable={editable}
                  value={chosen[split.index] ?? split.categoryId}
                  proposed={split.proposedCategoryId ?? split.categoryId}
                  changed={split.index in chosen}
                  onChange={(id) => onChoose(split.index, id)}
                />
                {split.memo ? <span className="change-review__memo">{split.memo}</span> : null}
              </li>
            ))}
          </ul>
        </>
      ) : null}

      {view.category ? (
        <div className="change-review__fields">
          <span className="hint hint--faint">Category</span>
          <CategoryField
            categories={rows}
            frequentIds={frequent}
            editable={editable}
            value={chosen[ROW_CATEGORY] ?? view.category.categoryId}
            proposed={view.category.proposedCategoryId ?? view.category.categoryId}
            changed={ROW_CATEGORY in chosen}
            onChange={(id) => onChoose(ROW_CATEGORY, id)}
          />
        </div>
      ) : null}

      {view.details.length > 0 ? (
        <div className="change-review__fields">
          {view.details.map((detail) => (
            <Fragment key={detail.label}>
              <span className="hint hint--faint">{detail.label}</span>
              <span className="change-review__value">
                {detail.kind === 'account' ? accountName(detail.value) : detail.value}
              </span>
            </Fragment>
          ))}
        </div>
      ) : null}
    </div>
  )
}

/** The index that stands for the change's own category rather than a split's. */
export const ROW_CATEGORY = -1

/** One empty list, so a card waiting on the category query does not rebuild
 *  everything below it on every render. */
const NO_CATEGORIES: readonly Category[] = []

/**
 * One category: what it will be filed under, what the model wanted when that
 * is no longer the same thing, and a picker while the card is still pending.
 */
function CategoryField({
  categories,
  frequentIds,
  editable,
  value,
  proposed,
  changed,
  onChange,
}: {
  categories: readonly Category[]
  frequentIds: readonly Uuid[]
  editable: boolean
  value: Uuid | null
  proposed: Uuid | null
  /** Whether this person has chosen something other than the proposal. */
  changed: boolean
  onChange: (id: Uuid | null) => void
}) {
  const label = categoryLabel(categories, value)
  // What the model asked for, shown only while it differs from the category
  // chosen, whether already applied or being changed now.
  const was = proposed !== null && proposed !== value ? categoryLabel(categories, proposed) : ''

  return (
    <span className="change-review__category">
      {editable ? (
        <CategoryPicker
          value={value}
          categories={categories}
          frequentIds={frequentIds}
          onChange={onChange}
          trigger={
            <button
              type="button"
              className="change-review__pick"
              data-empty={value === null}
              data-changed={changed}
              aria-label={`Category: ${label}. Choose a different one`}
            >
              {label}
            </button>
          }
        />
      ) : (
        <span className="change-review__named" data-empty={value === null}>
          {label}
        </span>
      )}
      {was ? <span className="hint hint--faint">it proposed {was}</span> : null}
    </span>
  )
}

/**
 * What the picker offers first: the categories this change names, since a
 * card has no rows to count.
 */
function frequentIds(view: ChangeView, categories: readonly Category[]): Uuid[] {
  const ids = view.splits.map((split) => split.categoryId)
  if (view.category) ids.push(view.category.categoryId)
  const known = new Set(categories.map((category) => category.id))
  return [...new Set(ids.filter((id): id is Uuid => id !== null && known.has(id)))]
}
