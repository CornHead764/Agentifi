import { useState } from 'react'

import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  Input,
  Textarea,
} from '@/components/ui'
import type { Guidance, GuidanceSubmit } from '@/lib/clients/guidance'
import { ConditionColumn } from './ConditionColumn'
import {
  EMPTY_CONDITIONS,
  buildConditions,
  readConditions,
  type ConditionDraft,
} from './conditions'
import type { Account, Category, Tag } from '@/lib/transactions/types'


/**
 * One standing instruction: what to say, and when. The left column is the
 * rules builder's condition column (ground rule 3), with the same floor of one
 * condition: a note with none would match nothing, so it is refused here.
 *
 * The instruction is prose that nothing parses; it is handed to the model
 * beside the row's facts. So there is no *Review existing transactions*: a
 * note changes no transaction.
 */
export interface GuidanceEditorProps {
  note: Guidance | null
  accounts: readonly Account[]
  categories: readonly Category[]
  tags: readonly Tag[]
  pending: boolean
  onSubmit: (body: GuidanceSubmit) => Promise<unknown>
  onClose: () => void
}

export function GuidanceEditor({
  note,
  accounts,
  categories,
  tags,
  pending,
  onSubmit,
  onClose,
}: GuidanceEditorProps) {
  // What a facet's stored encoding means — "uncategorized" is *not any of
  // every category*, and a chosen parent includes its children.
  const universe = { categories, tags }
  const [name, setName] = useState(note?.name ?? '')
  const [instruction, setInstruction] = useState(note?.instruction ?? '')
  const [conditions, setConditions] = useState<ConditionDraft>(() =>
    note === null ? EMPTY_CONDITIONS : readConditions(note.filter.items, universe),
  )

  const trimmedName = name.trim()
  const trimmedInstruction = instruction.trim()
  const items = buildConditions(conditions, universe)
  const editable = note === null || note.owns_filter
  // A note pointing at a shared filter keeps the conditions it has: they are
  // edited under the filter itself, where the other screens reading them show.
  const ready = trimmedName !== '' && trimmedInstruction !== '' && (!editable || items !== null)

  const submit = () => {
    if (!ready) return
    const body: GuidanceSubmit = {
      name: trimmedName,
      instruction: trimmedInstruction,
      ...(editable && items !== null ? { conditions: items } : {}),
    }
    void onSubmit(body).then(onClose, () => undefined)
  }

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        wide
        title={note === null ? 'New guidance' : `Edit ${note.name}`}
        description="Saving changes no transactions. The assistant reads this on matching ones."
        footer={
          <DialogActions>
            <Button variant="primary" onClick={submit} disabled={pending || !ready}>
              {note === null ? 'Create guidance' : 'Save'}
            </Button>
          </DialogActions>
        }
      >
        <div className="settings__form">
          <Field label="Guidance name" hint="Yours, for this list. The assistant sees it too.">
            <Input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Fuel Stop"
              autoComplete="off"
            />
          </Field>
        </div>

        <div className="rule-builder">
          <section className="stack stack--3 rule-builder__column">
            <h3 className="rule-builder__heading">If a transaction matches this</h3>
            {editable ? (
              <>
                <ConditionColumn
                  draft={conditions}
                  accounts={accounts}
                  categories={categories}
                  tags={tags}
                  onChange={setConditions}
                />
                {items === null ? (
                  <p className="hint">
                    A note needs at least one condition, or it is never read.
                  </p>
                ) : null}
              </>
            ) : (
              <p className="hint">
                Uses a shared saved filter. Edit its conditions on the filter itself.
              </p>
            )}
          </section>

          <section className="stack stack--3 rule-builder__column">
            <h3 className="rule-builder__heading">Tell the assistant this</h3>
            <Field
              label="Instruction"
              hint="Plain English: what decides the category."
            >
              <Textarea
                rows={10}
                value={instruction}
                onChange={(event) => setInstruction(event.target.value)}
                placeholder={
                  'One charge on the day under $20 is convenience-store food — file it as Fast Food. ' +
                  'Over $20 it is a tank of fuel — Gas & Fuel. Two charges on the same day: the ' +
                  'larger one is fuel and the smaller one is food.'
                }
              />
            </Field>
            <p className="hint">
              Read beside the payee&apos;s history. Your note wins when they disagree.
            </p>
          </section>
        </div>
      </DialogContent>
    </Dialog>
  )
}
