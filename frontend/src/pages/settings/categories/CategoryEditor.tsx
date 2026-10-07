import { Plus, X } from 'lucide-react'
import { useState } from 'react'

import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  IconButton,
  Input,
  OptionSelect,
  Switch,
} from '@/components/ui'
import type { CategoryCreate } from '@/lib/clients/categories'
import type { CategoryNode } from '@/lib/categoryTree'
import type { Category, CategoryKind, Uuid } from '@/lib/transactions/types'

import { CATEGORY_KINDS, readKind } from './kinds'
import { categoryPath, findCategoryNode, parentChoices } from './tree'
import { EXCLUDED_FROM_REPORTS, EXCLUDED_FROM_SPENDING_PLAN } from '@/lib/exclusions'

/** Radix refuses an empty string as an item value, and "no parent" needs one. */
const NO_PARENT = 'none'

/**
 * The codes a category holds, the Taxes report's first. A row can carry
 * `txf_id` without `txf_ids`, so neither alone is the list.
 */
function taxCodes(category: Category | null): string[] {
  if (category === null) return []
  const rest = category.txf_ids.filter((code) => code !== category.txf_id)
  return category.txf_id === null ? rest : [category.txf_id, ...rest]
}

export interface CategoryEditorProps {
  tree: readonly CategoryNode<Category>[]
  /** The category being edited, or null when this is a new one. */
  category: Category | null
  /** Where a new category starts out. Ignored when editing. */
  parentId?: Uuid | null
  pending: boolean
  /** Create or update is the caller's decision; the form is the same either way. */
  onSubmit: (body: CategoryCreate) => Promise<unknown>
  onClose: () => void
}

/**
 * The category editor. *Subcategory of* offers only parents that keep the
 * tree three deep, counting the moving branch's own subcategories.
 *
 * Tax codes are sent as both `txf_id` (the first, what the Taxes report groups
 * on) and `txf_ids`. Turning the toggle off sends `txf_id: null` and an empty
 * list, so no stale codes hide behind an unticked box.
 */
export function CategoryEditor({
  tree,
  category,
  parentId = null,
  pending,
  onSubmit,
  onClose,
}: CategoryEditorProps) {
  const moving = category === null ? null : findCategoryNode(tree, category.id)
  const choices = parentChoices(tree, moving)

  const startingParent = category === null ? parentId : category.parent_id
  const startingKind =
    category?.kind ??
    (startingParent === null
      ? 'expense'
      : (findCategoryNode(tree, startingParent)?.category.kind ?? 'expense'))

  const [name, setName] = useState(category?.name ?? '')
  const [parent, setParent] = useState<string>(startingParent ?? NO_PARENT)
  const [kind, setKind] = useState<CategoryKind>(startingKind)
  // An editable category with a reason to be kept is one a process finds by
  // its marker, and the API refuses changing its kind.
  const kindLocked = category?.protected_reason != null
  const [taxRelated, setTaxRelated] = useState(taxCodes(category).length > 0)
  const [codes, setCodes] = useState<string[]>(() => {
    const stored = taxCodes(category)
    return stored.length > 0 ? stored : ['']
  })
  const [fromReports, setFromReports] = useState(category?.excluded_from_reports ?? false)
  const [fromPlan, setFromPlan] = useState(category?.excluded_from_spending_plan ?? false)
  const [fromList, setFromList] = useState(category?.excluded_from_category_list ?? false)

  const trimmed = name.trim()

  const setCode = (index: number, value: string) =>
    setCodes((current) => current.map((one, at) => (at === index ? value : one)))
  const removeCode = (index: number) =>
    setCodes((current) => {
      const next = current.filter((_, at) => at !== index)
      return next.length > 0 ? next : ['']
    })

  const submit = () => {
    if (trimmed === '') return
    const written = taxRelated ? codes.map((one) => one.trim()).filter((one) => one !== '') : []
    const write: CategoryCreate = {
      name: trimmed,
      kind,
      parent_id: parent === NO_PARENT ? null : parent,
      txf_id: written[0] ?? null,
      txf_ids: written,
      excluded_from_reports: fromReports,
      excluded_from_spending_plan: fromPlan,
      excluded_from_category_list: fromList,
    }
    // A refusal is already a toast, and the dialog stays open holding what was
    // typed so it can be corrected rather than retyped.
    void onSubmit(write).then(onClose, () => undefined)
  }

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        title={category === null ? 'New category' : `Edit ${category.name}`}
        description="Type sets its spending plan bucket. Tax codes only feed the Taxes report."
        onSubmit={submit}
        footer={
          <DialogActions>
            <Button type="submit" variant="primary" disabled={pending || trimmed === ''}>
              {category === null ? 'Create category' : 'Save'}
            </Button>
          </DialogActions>
        }
      >
        <div className="settings__form">
          <Field label="Name">
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Groceries"
              autoComplete="off"
            />
          </Field>

          <Field
            label="Subcategory of"
            hint={
              choices.length === 0
                ? 'Nothing can hold this one without making the tree four levels deep.'
                : 'Leave it top level to make it a group of its own.'
            }
          >
            <OptionSelect
              value={parent}
              onValueChange={setParent}
              options={[
                { value: NO_PARENT, label: 'Top level' },
                ...choices.map((node) => ({
                  value: node.category.id,
                  label: categoryPath(tree, node.category.id),
                })),
              ]}
            />
          </Field>

          <Field
            label="Type"
            hint={
              kindLocked
                ? `The type is fixed because ${category?.protected_reason}.`
                : undefined
            }
          >
            <OptionSelect
              value={kind}
              onValueChange={(next) => setKind(readKind(next, kind))}
              options={CATEGORY_KINDS}
              disabled={kindLocked}
            />
          </Field>

          <Field label="Tax info" as="group">
            <Switch
              checked={taxRelated}
              onCheckedChange={setTaxRelated}
              label="Tax related"
              labelPosition="before"
            />
          </Field>

          {taxRelated ? (
            <Field
              label="Tax form & line item"
              as="group"
              hint="TXF codes, e.g. N269. The Taxes report groups on the first."
            >
              <div className="cat-codes">
                {codes.map((code, index) => (
                  // Keyed by index: keying by the text would remount the input
                  // on every keystroke.
                  <div className="cat-codes__row" key={index}>
                    <Input
                      value={code}
                      onChange={(event) => setCode(index, event.target.value)}
                      placeholder="N269"
                      aria-label={index === 0 ? 'Tax code' : `Tax code ${index + 1}`}
                      autoComplete="off"
                      spellCheck={false}
                    />
                    <IconButton
                      label={`Remove tax code ${index + 1}`}
                      variant="ghost"
                      size="sm"
                      onClick={() => removeCode(index)}
                    >
                      <X size={14} />
                    </IconButton>
                  </div>
                ))}
                <Button size="sm" onClick={() => setCodes((current) => [...current, ''])}>
                  <Plus size={13} /> Add a code
                </Button>
              </div>
            </Field>
          ) : null}

          {/* Not a `Field`: a field hands its one control id to everything
              inside it, and two switches sharing an id is two labels on one
              switch. */}
          <div className="field" role="group" aria-label="Exclusions">
            <span className="field__label">Exclusions</span>
            <Switch
              checked={fromReports}
              onCheckedChange={setFromReports}
              label={EXCLUDED_FROM_REPORTS.verb}
              labelPosition="before"
            />
            <Switch
              checked={fromPlan}
              onCheckedChange={setFromPlan}
              label={EXCLUDED_FROM_SPENDING_PLAN.verb}
              labelPosition="before"
            />
            {/* The third one is on the row menu as well, and an editor that
                sent only two would silently turn it off on every save. */}
            <Switch
              checked={fromList}
              onCheckedChange={setFromList}
              label="Exclude from the category picker"
              labelPosition="before"
            />
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
