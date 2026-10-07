import { clsx } from 'clsx'
import { Flag, Landmark, ListFilter, Shapes, Sigma, Tag as TagIcon, Users } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'

import { CategoryChecklist } from '@/components/CategoryChecklist'
import {
  Checkbox,
  Checklist,
  Field,
  MoneyInput,
  OptionSelect,
  Radio,
  RadioGroup,
  SearchableChecklist,
} from '@/components/ui'
import {
  REGISTER_FACETS,
  facetCategories,
  type FacetId,
} from '@/components/transactions/facets'
import { EXCLUDED_FROM_REPORTS, EXCLUDED_FROM_SPENDING_PLAN } from '@/lib/exclusions'
import { blankToNull } from '@/lib/format'
import { toAmountOperator } from '@/lib/reports/savedFilter'
import {
  amountBound,
  amountOperator,
  type AmountFacet,
  type FilterDraft,
  type IdFacet,
  type TextFacet,
} from '@/lib/transactions/filter'
import type { Account, Category, Tag, Uuid } from '@/lib/transactions/types'

/**
 * The facets of the one `Filter`, as controls: the one editor, mounted by the
 * register's Filter popover and by the rules and guidance builders. The caller
 * picks which facets to offer.
 */
const LABELS: Record<FacetId, { label: string; icon: ReactNode }> = {
  categories: { label: 'Categories', icon: <Shapes size={14} /> },
  payees: { label: 'Payees', icon: <Users size={14} /> },
  tags: { label: 'Tags', icon: <TagIcon size={14} /> },
  accounts: { label: 'Accounts', icon: <Landmark size={14} /> },
  flags: { label: 'Flags', icon: <Flag size={14} /> },
  amount: { label: 'Amount', icon: <Sigma size={14} /> },
  advanced: { label: 'Advanced', icon: <ListFilter size={14} /> },
}

export interface FilterFacetsProps {
  draft: FilterDraft
  categories: readonly Category[]
  tags: readonly Tag[]
  accounts: readonly Account[]
  /** Every payee in the space, with the loaded rows as a loading fallback. */
  payees?: readonly string[]
  /** Which facets to offer, in order. */
  facets?: readonly FacetId[]
  /**
   * Whether Advanced offers *Bills & Subscriptions*, *Suggested category* and
   * *Attachments*. The server refuses all three in a rule, a note or a trigger,
   * which run as a row arrives and see none of them.
   */
  offerVerdictFacets?: boolean
  /** Lay the facets out for a form column rather than the popover's two-pane grid. */
  compact?: boolean
  onChange: (draft: FilterDraft) => void
}

export function FilterFacets({
  draft,
  categories,
  tags,
  accounts,
  payees = [],
  facets = REGISTER_FACETS,
  offerVerdictFacets = true,
  compact = false,
  onChange,
}: FilterFacetsProps) {
  const [facet, setFacet] = useState<FacetId>(facets[0] ?? 'categories')
  const chosen = facets.includes(facet) ? facet : (facets[0] ?? 'categories')

  return (
    <>
      <div className={clsx('filter-panel__facets', compact && 'filter-panel__facets--wrap')}>
        {facets.map((id) => (
          <button
            key={id}
            type="button"
            className="filter-panel__facet"
            aria-selected={chosen === id}
            onClick={() => setFacet(id)}
          >
            {LABELS[id].icon}
            {LABELS[id].label}
          </button>
        ))}
      </div>

      <div className={clsx('filter-panel__body', compact && 'filter-panel__body--inline')}>
        {chosen === 'categories' ? (
          <CategoryFacet
            value={draft.categories}
            uncategorized={draft.uncategorized}
            undetermined={draft.categoryUndetermined}
            categories={categories}
            onChange={(categories_, uncategorized, categoryUndetermined) =>
              onChange({ ...draft, categories: categories_, uncategorized, categoryUndetermined })
            }
          />
        ) : null}

        {chosen === 'payees' ? (
          <TextOptions
            title="Payees"
            value={draft.payees}
            options={payees}
            empty="No payees in the rows loaded yet."
            onChange={(payees_) => onChange({ ...draft, payees: payees_ })}
          />
        ) : null}

        {chosen === 'tags' ? (
          <>
            <IdOptions
              title="Tags"
              value={draft.tags}
              options={tags.map((tag) => ({ id: tag.id, label: tag.name }))}
              empty="No tags yet."
              onChange={(tags_) => onChange({ ...draft, tags: tags_ })}
            />
            <TriState
              label="Tagged"
              value={draft.hasTags}
              yes="Has at least one tag"
              no="Has no tags"
              onChange={(hasTags) => onChange({ ...draft, hasTags })}
            />
          </>
        ) : null}

        {chosen === 'accounts' ? (
          <IdOptions
            title="Accounts"
            value={draft.accounts}
            options={accounts.map((account) => ({ id: account.id, label: account.name }))}
            empty="No accounts yet."
            onChange={(accounts_) => onChange({ ...draft, accounts: accounts_ })}
          />
        ) : null}

        {chosen === 'flags' ? (
          <TextOptions
            title="Flags"
            value={draft.flags}
            options={['flagged']}
            empty="No flags."
            onChange={(flags) => onChange({ ...draft, flags })}
          />
        ) : null}

        {chosen === 'amount' ? (
          <AmountOptions
            value={draft.amount}
            onChange={(amount) => onChange({ ...draft, amount })}
          />
        ) : null}

        {chosen === 'advanced' ? (
          <>
            <p className="filter-panel__section-title">Show only</p>
            {offerVerdictFacets ? (
              <TriState
                label="Bills & Subscriptions"
                value={draft.isBillOrSubscription}
                yes="Bills & Subscriptions"
                no="Not bills & subscriptions"
                onChange={(isBillOrSubscription) => onChange({ ...draft, isBillOrSubscription })}
              />
            ) : null}
            {/* Two flags, two independent controls. A single "excluded"
                question would make the pair impossible to express. */}
            <TriState
              label={EXCLUDED_FROM_REPORTS.name}
              value={draft.excludedFromReports}
              yes={EXCLUDED_FROM_REPORTS.state}
              no={EXCLUDED_FROM_REPORTS.notState}
              onChange={(excludedFromReports) => onChange({ ...draft, excludedFromReports })}
            />
            <TriState
              label={EXCLUDED_FROM_SPENDING_PLAN.name}
              value={draft.excludedFromSpendingPlan}
              yes={EXCLUDED_FROM_SPENDING_PLAN.state}
              no={EXCLUDED_FROM_SPENDING_PLAN.notState}
              onChange={(excludedFromSpendingPlan) =>
                onChange({ ...draft, excludedFromSpendingPlan })
              }
            />
            <TriState
              label="Review"
              value={draft.isReviewed}
              yes="Reviewed"
              no="Not reviewed"
              onChange={(isReviewed) => onChange({ ...draft, isReviewed })}
            />
            {offerVerdictFacets ? (
              <TriState
                label="Suggested category"
                value={draft.hasCategorySuggestion}
                yes="Has a suggestion"
                no="No suggestion"
                onChange={(hasCategorySuggestion) => onChange({ ...draft, hasCategorySuggestion })}
              />
            ) : null}
            {offerVerdictFacets ? (
              <TriState
                label="Attachments"
                value={draft.hasAttachment}
                yes="Has attachments"
                no="No attachments"
                onChange={(hasAttachment) => onChange({ ...draft, hasAttachment })}
              />
            ) : null}
            {offerVerdictFacets ? (
              <TriState
                label="Receipts"
                value={draft.missingReceipt}
                yes="Missing a receipt"
                no="Not missing one"
                onChange={(missingReceipt) => onChange({ ...draft, missingReceipt })}
              />
            ) : null}
            <TriState
              label="Settled"
              value={draft.isPending}
              yes="Still pending"
              no="Settled"
              onChange={(isPending) => onChange({ ...draft, isPending })}
            />
          </>
        ) : null}
      </div>
    </>
  )
}

/**
 * An amount, as a direction and a comparison on its size. The operator is kept
 * on the facet so *equals* does not reopen as *between 20 and 20*.
 */
function AmountOptions({
  value,
  onChange,
}: {
  value: AmountFacet | null
  onChange: (value: AmountFacet | null) => void
}) {
  const operator = value === null ? 'equals' : amountOperator(value)
  const lower = value?.min ?? ''
  const upper = value?.max ?? ''

  const set = (next: Partial<AmountFacet> & { operator?: AmountFacet['operator'] }) => {
    const merged: AmountFacet = {
      min: value?.min ?? null,
      max: value?.max ?? null,
      direction: value?.direction ?? 'expense',
      operator,
      ...next,
    }
    // Bounds the operator does not use are cleared, or they would narrow by a
    // number nobody can see.
    if (merged.operator === 'less_than') merged.min = null
    if (merged.operator !== 'less_than' && merged.operator !== 'between') merged.max = null
    onChange(merged)
  }

  return (
    <>
      <p className="filter-panel__section-title">Amount</p>
      <Checkbox
        label="Narrow by amount"
        checked={value !== null}
        onCheckedChange={(checked) =>
          onChange(
            checked === true
              ? { min: null, max: null, direction: 'expense', operator: 'equals' }
              : null,
          )
        }
      />
      {value === null ? null : (
        <>
          <Field label="Direction" as="group">
            <RadioGroup
              value={value.direction ?? 'either'}
              onValueChange={(next) =>
                set({ direction: next === 'either' ? null : next === 'income' ? 'income' : 'expense' })
              }
            >
              <Radio value="expense" label="Expense" />
              <Radio value="income" label="Income" />
              <Radio value="either" label="Either" />
            </RadioGroup>
          </Field>
          <Field label="Amount is" hint="Compared against the size of the amount, not its sign.">
            <OptionSelect
              value={operator}
              onValueChange={(next) => set({ operator: toAmountOperator(next) })}
              options={[
                { value: 'equals', label: 'Equals' },
                { value: 'between', label: 'Between' },
                { value: 'greater_than', label: 'Greater than' },
                { value: 'less_than', label: 'Less than' },
              ]}
            />
          </Field>
          <div className="rule-builder__pair">
            <Field
              label={operator === 'between' ? 'From' : 'Value'}
              error={boundError(operator === 'less_than' ? upper : lower)}
            >
              <MoneyInput
                value={operator === 'less_than' ? upper : lower}
                onChange={(event) =>
                  set(
                    operator === 'less_than'
                      ? { max: blankToNull(event.target.value) }
                      : { min: blankToNull(event.target.value) },
                  )
                }
                placeholder="15.00"
              />
            </Field>
            {operator === 'between' ? (
              <Field label="To" error={boundError(upper)}>
                <MoneyInput
                  value={upper}
                  onChange={(event) => set({ max: blankToNull(event.target.value) })}
                  placeholder="20.00"
                />
              </Field>
            ) : null}
          </div>
        </>
      )}
    </>
  )
}

/** A typed bound that is not an amount would be sent as no bound at all. */
function boundError(typed: string): string | undefined {
  return typed.trim() === '' || amountBound(typed) !== null ? undefined : 'Not an amount.'
}

function CategoryFacet({
  value,
  uncategorized,
  undetermined,
  categories,
  onChange,
}: {
  value: IdFacet
  uncategorized: boolean
  /** Uncategorized *and* the assistant has already given up on it. */
  undetermined: boolean
  categories: readonly Category[]
  onChange: (value: IdFacet, uncategorized: boolean, undetermined: boolean) => void
}) {
  const assignable = useMemo(() => facetCategories(categories), [categories])

  return (
    <>
      <CategoryChecklist
        categories={assignable}
        selected={value.ids}
        onChange={(ids) => onChange({ ...value, ids }, uncategorized, undetermined)}
        extraRows={[
          {
            id: 'uncategorized',
            label: 'Uncategorized',
            checked: uncategorized,
            onCheckedChange: (checked) => onChange(value, checked, undetermined),
          },
          // A child of Uncategorized: every undetermined row is uncategorized.
          {
            id: 'undetermined',
            label: 'Undetermined',
            checked: undetermined,
            depth: 1,
            onCheckedChange: (checked) => onChange(value, uncategorized, checked),
          },
        ]}
      />
      <NegateToggle
        checked={value.negated}
        label="Exclude the chosen categories instead"
        onChange={(negated) => onChange({ ...value, negated }, uncategorized, undetermined)}
      />
    </>
  )
}

function IdOptions({
  title,
  value,
  options,
  empty,
  onChange,
}: {
  title: string
  value: IdFacet
  options: readonly { id: Uuid; label: string }[]
  empty: string
  onChange: (value: IdFacet) => void
}) {
  return (
    <>
      <p className="filter-panel__section-title">{title}</p>
      <Checklist
        options={options}
        chosen={value.ids}
        onChange={(ids) => onChange({ ...value, ids })}
        empty={empty}
      />
      {options.length > 0 ? (
        <NegateToggle
          checked={value.negated}
          label={`Exclude the chosen ${title.toLowerCase()} instead`}
          onChange={(negated) => onChange({ ...value, negated })}
        />
      ) : null}
    </>
  )
}

function TextOptions({
  title,
  value,
  options,
  empty,
  onChange,
}: {
  title: string
  value: TextFacet
  options: readonly string[]
  empty: string
  onChange: (value: TextFacet) => void
}) {
  const choices = useMemo(
    () => options.map((option) => ({ id: option, label: option, text: option })),
    [options],
  )

  return (
    <>
      <p className="filter-panel__section-title">{title}</p>
      <SearchableChecklist
        options={choices}
        chosen={value.values}
        onChange={(values) => onChange({ ...value, values })}
        searchLabel={`Search ${title.toLowerCase()}`}
        empty={empty}
        limit={200}
      />
    </>
  )
}

/** A radio pair plus the "don't care" the pair alone cannot express. */
function TriState({
  label,
  value,
  yes,
  no,
  onChange,
}: {
  label: string
  value: boolean | null
  yes: string
  no: string
  onChange: (value: boolean | null) => void
}) {
  return (
    <div>
      <p className="filter-panel__section-title">{label}</p>
      <RadioGroup
        value={value === null ? 'any' : value ? 'yes' : 'no'}
        onValueChange={(next) => onChange(next === 'any' ? null : next === 'yes')}
      >
        <Radio value="any" label="Any" />
        <Radio value="yes" label={yes} />
        <Radio value="no" label={no} />
      </RadioGroup>
    </div>
  )
}

function NegateToggle({
  checked,
  label,
  onChange,
}: {
  checked: boolean
  label: string
  onChange: (checked: boolean) => void
}) {
  return (
    <div className="option-list__row">
      <Checkbox
        label={label}
        checked={checked}
        onCheckedChange={(next) => onChange(next === true)}
      />
    </div>
  )
}
