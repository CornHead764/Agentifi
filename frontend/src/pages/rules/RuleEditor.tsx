import { useState } from 'react'

import {
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  EmptyState,
  Field,
  Input,
  Radio,
  RadioGroup,
  Textarea,
} from '@/components/ui'
import { CategoryPicker } from '@/components/transactions/Pickers'
import type { Rule, RuleCreate } from '@/lib/clients/rules'
import type { Account, Category, Tag, Uuid } from '@/lib/transactions/types'

import { ConditionColumn } from './ConditionColumn'
import { EXCLUDED_FROM_REPORTS, EXCLUDED_FROM_SPENDING_PLAN } from '@/lib/exclusions'
import { toggled } from '@/lib/toggle'
import {
  EMPTY_CONDITIONS,
  buildConditions,
  readConditions,
  seedConditions,
  type ConditionDraft,
  type RuleSeed,
} from './conditions'

/**
 * The two-column builder: *If a transaction matches this* ⁄ *Then make these
 * changes*.
 *
 * **Original statement name is the default field.** A rule matched on the
 * payee renames its own input and stops matching, which is why the choice
 * says so.
 *
 * **Every action is three-state.** *Leave alone* is not *Include everywhere*:
 * one has no opinion, the other clears the flag. The two exclusions are asked
 * separately, with no control that answers both.
 *
 * **Saving does not touch history.** Only *Review existing transactions*
 * reaches rows that already exist.
 */
export interface RuleEditorProps {
  rule: Rule | null
  /** The transaction a create was started from, in the register. Ignored while editing. */
  seed?: RuleSeed | null
  accounts: readonly Account[]
  categories: readonly Category[]
  tags: readonly Tag[]
  pending: boolean
  onSubmit: (body: RuleCreate) => Promise<unknown>
  onClose: () => void
}

export function RuleEditor({
  rule,
  seed = null,
  accounts,
  categories,
  tags,
  pending,
  onSubmit,
  onClose,
}: RuleEditorProps) {
  // The universe lets a facet mean what the register means (see
  // `buildConditions`).
  const universe = { categories, tags }
  const [name, setName] = useState(rule?.name ?? seed?.payee ?? '')
  const [conditions, setConditions] = useState<ConditionDraft>(() => {
    if (rule !== null) return readConditions(rule.filter.items, universe)
    return seed === null ? EMPTY_CONDITIONS : seedConditions(seed)
  })
  const [payee, setPayee] = useState(rule?.actions.set_payee ?? '')
  // A rule needs an action to save, so a seeded create opens on filing the
  // next charge like this one; left alone when the row is uncategorized.
  const [categoryId, setCategoryId] = useState<Uuid | null>(
    rule?.actions.set_category_id ?? seed?.category_id ?? null,
  )
  const [tagIds, setTagIds] = useState<Uuid[]>(rule?.actions.add_tag_ids ?? [])
  const [note, setNote] = useState(rule?.actions.set_notes ?? '')
  const [fromReports, setFromReports] = useState(rule?.actions.set_excluded_from_reports ?? null)
  const [fromPlan, setFromPlan] = useState(rule?.actions.set_excluded_from_spending_plan ?? null)
  const [reviewed, setReviewed] = useState(rule?.actions.set_is_reviewed ?? null)

  const trimmedName = name.trim()
  const items = buildConditions(conditions, universe)
  const actions = {
    set_payee: payee.trim() === '' ? null : payee.trim(),
    set_category_id: categoryId,
    add_tag_ids: tagIds,
    set_notes: note.trim() === '' ? null : note.trim(),
    set_excluded_from_reports: fromReports,
    set_excluded_from_spending_plan: fromPlan,
    set_is_reviewed: reviewed,
  }
  const acts =
    actions.set_payee !== null ||
    actions.set_category_id !== null ||
    actions.set_notes !== null ||
    actions.add_tag_ids.length > 0 ||
    actions.set_excluded_from_reports !== null ||
    actions.set_excluded_from_spending_plan !== null ||
    actions.set_is_reviewed !== null

  const ready = trimmedName !== '' && items !== null && acts
  const editable = rule === null || rule.owns_filter

  const submit = () => {
    if (!ready || items === null) return
    void onSubmit({ name: trimmedName, conditions: items, actions }).then(onClose, () => undefined)
  }

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        wide
        title={rule === null ? 'New rule' : `Edit ${rule.name}`}
        description="Saving leaves existing transactions alone. Review them afterwards to apply it."
        footer={
          <DialogActions>
            <Button variant="primary" onClick={submit} disabled={pending || !ready}>
              {rule === null ? 'Create rule' : 'Save'}
            </Button>
          </DialogActions>
        }
      >
        <div className="settings__form">
          <Field label="Rule name">
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Example Streaming"
              autoComplete="off"
            />
          </Field>
        </div>

        <div className="rule-builder">
          <section className="stack stack--3 rule-builder__column">
            <h3 className="rule-builder__heading">If a transaction matches this</h3>
            {editable ? (
              <ConditionColumn
                draft={conditions}
                accounts={accounts}
                categories={categories}
                tags={tags}
                onChange={setConditions}
              />
            ) : (
              <p className="hint">
                Uses a shared saved filter. Edit its conditions on the filter itself.
              </p>
            )}
            {items === null && editable ? (
              <p className="hint">
                A rule needs at least one condition.
              </p>
            ) : null}
          </section>

          <section className="stack stack--3 rule-builder__column">
            <h3 className="rule-builder__heading">Then make these changes</h3>

            <Field label="Rename payee" hint="The bank's own wording is never overwritten.">
              <Input
                value={payee}
                onChange={(event) => setPayee(event.target.value)}
                placeholder="Leave alone"
                autoComplete="off"
              />
            </Field>

            <Field label="Category">
              <CategoryPicker
                value={categoryId}
                categories={categories}
                frequentIds={[]}
                noneLabel="Leave alone"
                onChange={setCategoryId}
                trigger={
                  <button type="button" className="select__trigger">
                    {categories.find((category) => category.id === categoryId)?.name ??
                      'Leave alone'}
                  </button>
                }
              />
            </Field>

            <Field label="Add tags" as="group">
              {tags.length === 0 ? (
                <EmptyState compact title="No tags yet." />
              ) : (
                <div className="rule-builder__checks">
                  {tags.map((tag) => (
                    <Checkbox
                      key={tag.id}
                      label={tag.name}
                      checked={tagIds.includes(tag.id)}
                      onCheckedChange={(checked) =>
                        setTagIds((current) => toggled(current, tag.id, checked === true))
                      }
                    />
                  ))}
                </div>
              )}
            </Field>

            <Field label="Add note" hint="Appended to whatever the note already says.">
              <Textarea
                rows={2}
                value={note}
                onChange={(event) => setNote(event.target.value)}
                placeholder="Leave alone"
              />
            </Field>

            <TriState
              label={EXCLUDED_FROM_SPENDING_PLAN.name}
              hint={EXCLUDED_FROM_SPENDING_PLAN.reach}
              excludeLabel={EXCLUDED_FROM_SPENDING_PLAN.verb}
              value={fromPlan}
              onChange={setFromPlan}
            />
            <TriState
              label={EXCLUDED_FROM_REPORTS.name}
              hint={EXCLUDED_FROM_REPORTS.reach}
              excludeLabel={EXCLUDED_FROM_REPORTS.verb}
              value={fromReports}
              onChange={setFromReports}
            />
            <TriState
              label="Reviewed"
              excludeLabel="Mark as reviewed"
              includeLabel="Mark as unreviewed"
              value={reviewed}
              onChange={setReviewed}
            />
          </section>
        </div>
      </DialogContent>
    </Dialog>
  )
}

/**
 * One action that can leave a flag alone, set it, or clear it. Not a switch:
 * collapsing "no opinion" into "off" is how a rule meant only to categorize
 * starts pulling rows back into reports.
 */
function TriState({
  label,
  hint,
  excludeLabel,
  includeLabel = 'Include everywhere',
  value,
  onChange,
}: {
  label: string
  hint?: string
  excludeLabel: string
  includeLabel?: string
  value: boolean | null
  onChange: (value: boolean | null) => void
}) {
  return (
    <Field label={label} hint={hint} as="group">
      <RadioGroup
        value={value === null ? 'leave' : value ? 'set' : 'clear'}
        onValueChange={(next) => onChange(next === 'leave' ? null : next === 'set')}
      >
        <Radio value="leave" label="Leave alone" />
        <Radio value="set" label={excludeLabel} />
        <Radio value="clear" label={includeLabel} />
      </RadioGroup>
    </Field>
  )
}

