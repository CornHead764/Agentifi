import { Eye, FlaskConical } from 'lucide-react'
import { useState } from 'react'

import { InfoTip } from '@/components/InfoTip'
import { FilterFacets } from '@/components/transactions/FilterFacets'
import { TRIGGER_FACETS } from '@/components/transactions/facets'
import {
  Button,
  Callout,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  Input,
  OptionSelect,
  Radio,
  RadioGroup,
  Textarea,
} from '@/components/ui'
import { writeToolNames, type AssistantStatus } from '@/lib/clients/assistant'
import {
  AUTOMATION_MODES,
  asMode,
  asTrigger,
  triggerConditions,
  triggerDraft,
  useCreateAutomation,
  usePreviewAutomation,
  useRunAutomation,
  useUpdateAutomation,
  type Automation,
  type AutomationForm,
  type AutomationMode,
  type AutomationPreview,
  type AutomationTemplate,
} from '@/lib/clients/automations'
import type { FilterDraft, FilterUniverse } from '@/lib/transactions/filter'
import { useAccounts, useCategories, usePayees, useTags } from '@/lib/transactions/queries'
import type { Uuid } from '@/lib/transactions/types'

import { TurnOnChangesButton } from './connection'

/** What the editor opens on: an existing automation, a template, or nothing. */
export interface EditorSeed {
  automation?: Automation
  template?: AutomationTemplate
}

type Draft = Required<Omit<AutomationForm, 'conditions'>>

const BLANK: Draft = {
  name: '',
  description: '',
  is_enabled: true,
  trigger: 'transaction_arrived',
  trigger_config: { skip_transfers: true },
  prompt: '',
  context: {
    transaction: true,
    similar_transactions: 10,
    categories: true,
    rules: false,
    accounts: false,
    corrections: true,
    guidance: true,
    cash_flow_history: false,
  },
  mode: 'propose',
  tools: [],
  model: '',
  max_tool_rounds: 6,
  confidence_threshold: 0.85,
}

function seedDraft(seed: EditorSeed): Draft {
  const from = seed.automation ?? seed.template
  if (!from) return BLANK
  return {
    ...BLANK,
    name: from.name,
    description: from.description,
    is_enabled: seed.automation?.is_enabled ?? true,
    trigger: from.trigger,
    trigger_config: { ...from.trigger_config },
    prompt: from.prompt,
    context: { ...BLANK.context, ...from.context },
    mode: from.mode,
    tools: [...from.tools],
    model: seed.automation?.model ?? '',
    max_tool_rounds: seed.automation?.max_tool_rounds ?? 6,
    confidence_threshold: from.confidence_threshold,
  }
}

const CONTEXT_CHECKS: readonly [Exclude<keyof Draft['context'], 'similar_transactions'>, string][] = [
  ['transaction', 'the transaction itself'],
  ['categories', 'the category tree, with ids'],
  ['rules', "the space's rules"],
  ['accounts', 'the account list'],
  ['corrections', 'the categories you changed on its earlier proposals'],
  ['guidance', 'your standing guidance about rows like this one'],
  ['cash_flow_history', '12 months of money in and out (dates and amounts only)'],
]

const MODE_TEXT: Record<AutomationMode, { label: string; hint: string }> = {
  observe: {
    label: 'Observe only',
    hint: 'It reads and reports. Nothing is proposed or changed.',
  },
  propose: {
    label: 'Propose changes',
    hint: 'Each change waits on its transaction to accept or decline.',
  },
  apply: {
    label: 'Apply changes immediately',
    hint: 'Changes are made unattended and recorded on the run.',
  },
}

/**
 * Everything about an automation, on one form. The system prompt around the
 * prompt is a button away, so every word the model will be given can be read
 * before saving.
 */
export function AutomationEditor({
  seed,
  status,
  onClose,
  onOpenRun,
}: {
  seed: EditorSeed | null
  status: AssistantStatus
  onClose: () => void
  /** Called with a finished dry run's id, so the caller can show it. */
  onOpenRun: (id: Uuid) => void
}) {
  return (
    <Dialog open={seed !== null} onOpenChange={(open) => !open && onClose()}>
      {seed ? (
        <EditorBody
          seed={seed}
          status={status}
          onClose={onClose}
          onOpenRun={onOpenRun}
        />
      ) : null}
    </Dialog>
  )
}

function EditorBody({
  seed,
  status,
  onClose,
  onOpenRun,
}: {
  seed: EditorSeed
  status: AssistantStatus
  onClose: () => void
  onOpenRun: (id: Uuid) => void
}) {
  const [draft, setDraft] = useState<Draft>(() => seedDraft(seed))
  // Set once created, so a new automation can be previewed without closing
  // and reopening: the preview needs a saved row to render against.
  const [savedId, setSavedId] = useState<Uuid | null>(seed.automation?.id ?? null)
  const [preview, setPreview] = useState<AutomationPreview | null>(null)
  const create = useCreateAutomation()
  const update = useUpdateAutomation()
  const previewer = usePreviewAutomation()
  const dryRunner = useRunAutomation()
  const accounts = useAccounts()
  const categories = useCategories()
  const tags = useTags()
  const payees = usePayees()
  const universe: FilterUniverse = { categories: categories.data ?? [], tags: tags.data ?? [] }
  // Which rows it fires on, as the one filter's facets. Seeded once from the
  // stored filter; a later load of the category list does not reset an edit.
  const [facets, setFacets] = useState<FilterDraft>(() => triggerDraft(seed.automation, universe))

  const patch = (changes: Partial<Draft>) => {
    setDraft((prev) => ({ ...prev, ...changes }))
    setPreview(null)
  }
  const formOf = (): AutomationForm => ({
    ...draft,
    name: draft.name.trim(),
    prompt: draft.prompt.trim(),
    ...triggerConditions(draft.trigger, facets, universe),
  })
  const writeTools = writeToolNames(status.tools)
  const setMode = (mode: AutomationMode) =>
    patch({
      mode,
      // An observing automation cannot hold a change tool; the server refuses
      // the pair, so the form drops them rather than saving into a refusal.
      tools: mode === 'observe' ? draft.tools.filter((name) => !writeTools.has(name)) : draft.tools,
    })

  const busy = create.isPending || update.isPending
  const valid =
    draft.name.trim() !== '' &&
    draft.prompt.trim() !== '' &&
    (draft.trigger !== 'daily' || /^\d\d:\d\d$/.test(draft.trigger_config.at ?? ''))

  const save = (andClose: boolean) => {
    const form = formOf()
    if (savedId) {
      update.mutate({ id: savedId, ...form }, { onSuccess: () => andClose && onClose() })
    } else {
      create.mutate(form, {
        onSuccess: (made) => {
          setSavedId(made.id)
          if (andClose) onClose()
        },
      })
    }
  }

  const showPreview = () => {
    if (!savedId) return
    previewer.mutate({ id: savedId, ...draft }, { onSuccess: setPreview })
  }

  // Saved first, then run: a dry run exercises the stored automation, not
  // what is on screen.
  const dryRun = () => {
    if (!savedId || !valid) return
    const form = formOf()
    update.mutate(
      { id: savedId, ...form },
      {
        onSuccess: () =>
          dryRunner.mutate(
            { id: savedId, dry_run: true },
            { onSuccess: (result) => !Array.isArray(result) && onOpenRun(result.id) },
          ),
      },
    )
  }

  const changesOff = !status.allow_writes && draft.mode !== 'observe'

  return (
    <DialogContent
      wide
      title={seed.automation ? `Edit ${seed.automation.name}` : 'New automation'}
      description="What the assistant is told, when it runs, and how far it may go."
      onSubmit={(event) => {
        event.preventDefault()
        if (valid && !busy) save(true)
      }}
      footer={
        <DialogActions
          start={
            <>
              <Button
                type="button"
                variant="ghost"
                disabled={!savedId || previewer.isPending}
                title={savedId ? undefined : 'Save once to preview the rendered prompt'}
                onClick={showPreview}
              >
                <Eye size={13} aria-hidden="true" />{' '}
                {previewer.isPending ? 'Rendering…' : 'Preview prompt'}
              </Button>
              <Button
                type="button"
                variant="ghost"
                disabled={!savedId || !valid || busy || dryRunner.isPending}
                title={
                  savedId
                    ? 'Saves, then runs on the latest matching transaction without changing anything'
                    : 'Create it once to dry-run it'
                }
                onClick={dryRun}
              >
                <FlaskConical size={13} aria-hidden="true" />{' '}
                {dryRunner.isPending ? 'Running…' : 'Dry run'}
              </Button>
            </>
          }
        >
          <Button type="submit" variant="primary" disabled={!valid || busy}>
            {busy ? 'Saving…' : savedId ? 'Save' : 'Create automation'}
          </Button>
        </DialogActions>
      }
    >
      <div className="automation-editor">
        <Field label="Name">
          <Input
            value={draft.name}
            placeholder="Suggest categories"
            onChange={(event) => patch({ name: event.target.value })}
          />
        </Field>
        <Field label="Description" hint="For the list. The model never sees this.">
          <Input
            value={draft.description}
            onChange={(event) => patch({ description: event.target.value })}
          />
        </Field>

        <Field label="Runs" as="group">
          <OptionSelect
            value={draft.trigger}
            onValueChange={(value) => {
              const trigger = asTrigger(value)
              if (trigger) patch({ trigger })
            }}
            options={[
              {
                value: 'transaction_arrived',
                label: 'When a transaction arrives from the bank or a file',
              },
              { value: 'daily', label: 'Once a day' },
              { value: 'manual', label: 'Only when I press Run' },
            ]}
          />
        </Field>

        {draft.trigger === 'daily' ? (
          <Field label="At" hint="Space time zone. A missed run catches up.">
            <Input
              type="time"
              value={draft.trigger_config.at ?? '06:00'}
              onChange={(event) =>
                patch({ trigger_config: { ...draft.trigger_config, at: event.target.value } })
              }
            />
          </Field>
        ) : null}

        {draft.trigger === 'transaction_arrived' ? (
          <Field
            label="Only for"
            as="group"
            className="automation-editor__span"
            hint="Leave every facet empty to fire on every row."
          >
            <Checkbox
              label="rows that are not a transfer leg"
              checked={draft.trigger_config.skip_transfers ?? false}
              onCheckedChange={(checked) =>
                patch({ trigger_config: { ...draft.trigger_config, skip_transfers: checked === true } })
              }
            />
            <div className="rule-builder__facets">
              <FilterFacets
                draft={facets}
                categories={universe.categories}
                tags={universe.tags}
                accounts={accounts.data ?? []}
                payees={payees.data ?? []}
                facets={TRIGGER_FACETS}
                offerVerdictFacets={false}
                compact
                onChange={(next) => {
                  setFacets(next)
                  setPreview(null)
                }}
              />
            </div>
          </Field>
        ) : null}

        <Field
          label="Instructions"
          className="automation-editor__span"
          hint="Added after the assistant's standing rules. Preview shows the full prompt."
        >
          <Textarea
            rows={10}
            value={draft.prompt}
            onChange={(event) => patch({ prompt: event.target.value })}
          />
        </Field>

        <Field label="How far it may go" as="group" className="automation-editor__span">
          <RadioGroup
            value={draft.mode}
            onValueChange={(value) => {
              const mode = asMode(value)
              if (mode) setMode(mode)
            }}
          >
            {AUTOMATION_MODES.map((mode) => (
              <div key={mode} className="automation-editor__mode">
                <Radio value={mode} label={MODE_TEXT[mode].label} />
                <p className="hint">{MODE_TEXT[mode].hint}</p>
              </div>
            ))}
          </RadioGroup>
          {changesOff ? (
            <Callout tone="warning" actions={<TurnOnChangesButton status={status} />}>
              Changes are off for the assistant, so this would fail. Turn them on, or choose Observe
              only.
            </Callout>
          ) : null}
        </Field>

        <details className="automation-editor__span run-detail__fold">
          <summary>Advanced</summary>
          <div className="automation-editor">
            <Field
              label="Hand it up front"
              as="group"
              className="automation-editor__span"
              hint="Put in the opening message. It can still look up anything else."
            >
              <div className="automation-editor__checks automation-editor__checks--columns">
                {CONTEXT_CHECKS.map(([key, label]) => (
                  <Checkbox
                    key={key}
                    label={label}
                    disabled={key === 'transaction' && draft.trigger === 'daily'}
                    checked={draft.context[key]}
                    onCheckedChange={(checked) =>
                      patch({ context: { ...draft.context, [key]: checked === true } })
                    }
                  />
                ))}
              </div>
              <Field
                label="Similar past transactions from the same payee"
                hint="With their categories. Zero for none. The confidence score is computed from these."
              >
                <Input
                  type="number"
                  min={0}
                  max={50}
                  disabled={draft.trigger === 'daily'}
                  value={draft.context.similar_transactions}
                  onChange={(event) =>
                    patch({
                      context: {
                        ...draft.context,
                        similar_transactions: Math.max(0, Math.min(50, Number(event.target.value) || 0)),
                      },
                    })
                  }
                />
              </Field>
            </Field>

            <Field
              label="Act from the history alone at confidence"
              hint={
                <>
                  At or above: acts without the model. Zero: always asks the model.
                  <InfoTip>
                    The score weighs how much of the payee&apos;s past categorizations agree, how many
                    rows agree, and how much of its history was ever categorized. A payee whose rows
                    are transfer legs is left alone. Below the threshold the model decides, with the
                    score in front of it.
                  </InfoTip>
                </>
              }
            >
              <Input
                type="number"
                min={0}
                max={1}
                step={0.05}
                disabled={draft.trigger === 'daily' || draft.context.similar_transactions === 0}
                value={draft.confidence_threshold}
                onChange={(event) =>
                  patch({
                    confidence_threshold: Math.max(0, Math.min(1, Number(event.target.value) || 0)),
                  })
                }
              />
            </Field>
            <Field
              label="Tools it may use"
              as="group"
              className="automation-editor__span"
              hint="None ticked: every tool the mode allows."
            >
              <div className="automation-editor__checks automation-editor__checks--columns">
                {status.tools.map((tool) => {
                  const disabled = tool.writes && draft.mode === 'observe'
                  const on = draft.tools.includes(tool.name)
                  return (
                    <Checkbox
                      key={tool.name}
                      label={
                        <span title={tool.description}>
                          {tool.name.replace(/_/g, ' ')}
                          {tool.writes ? <span className="muted"> · changes</span> : null}
                        </span>
                      }
                      disabled={disabled}
                      checked={on && !disabled}
                      onCheckedChange={(checked) =>
                        patch({
                          tools:
                            checked === true
                              ? [...draft.tools, tool.name]
                              : draft.tools.filter((name) => name !== tool.name),
                        })
                      }
                    />
                  )
                })}
              </div>
            </Field>

            <Field label="Model" hint={`Blank uses the connection's model, ${status.model}.`}>
              <Input
                value={draft.model}
                placeholder={status.model}
                onChange={(event) => patch({ model: event.target.value })}
              />
            </Field>
            <Field label="Tool rounds" hint="How many times it may call tools before it has to answer.">
              <Input
                type="number"
                min={1}
                max={20}
                value={draft.max_tool_rounds}
                onChange={(event) =>
                  patch({ max_tool_rounds: Math.max(1, Math.min(20, Number(event.target.value) || 1)) })
                }
              />
            </Field>
          </div>
        </details>

        {preview ? (
          <div className="automation-editor__span automation-preview">
            <h3>What it will be told</h3>
            <p className="muted">
              The system prompt, then the opening message
              {preview.transaction_id ? ', rendered against the most recent matching transaction' : ''}.
              Tools offered: {preview.tools.length === 0 ? 'none' : preview.tools.join(', ')}.
            </p>
            <pre className="automation-preview__text">{preview.prompt}</pre>
            <pre className="automation-preview__text">{preview.opening}</pre>
          </div>
        ) : null}
      </div>
    </DialogContent>
  )
}
