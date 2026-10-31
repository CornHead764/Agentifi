/**
 * The goal editor, create and edit in one dialog. A goal writes its whole form
 * on save.
 *
 * Cash accounts only: a goal's reserve subtracts from an *available* balance,
 * which credit, loan, investment and asset accounts do not carry.
 */

import { ArrowLeft, Plus } from 'lucide-react'
import { useState } from 'react'

import { NewAccountDialog } from '@/components/shell/NewAccountDialog'
import {
  Button,
  Checkbox,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  IconButton,
  Input,
  MoneyInput,
  Switch,
  useToast,
} from '@/components/ui'
import { AccountSelect } from '@/components/AccountSelect'
import {
  GOAL_TEMPLATES,
  blankGoalEdits,
  editsFromGoal,
  goalBodyFromEdits,
  useCreateGoal,
  useUpdateGoal,
  type Goal,
  type GoalEdits,
  type GoalTemplate,
} from '@/lib/goals'

export interface GoalEditorProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The goal being edited. Null creates one. */
  goal?: Goal | null
  /** Cash accounts only — see the module comment. */
  accounts: readonly { id: string; name: string }[]
}

export function GoalEditor({ open, onOpenChange, goal = null, accounts }: GoalEditorProps) {
  return (
    <FormDialog open={open} onOpenChange={onOpenChange}>
      <GoalFlow goal={goal} accounts={accounts} onDone={onOpenChange} />
    </FormDialog>
  )
}

/**
 * Create opens on the question, edit on the form: asking *What are you saving
 * for?* of an existing goal would offer to overwrite its name.
 */
function GoalFlow({
  goal,
  accounts,
  onDone,
}: {
  goal: Goal | null
  accounts: readonly { id: string; name: string }[]
  onDone: (open: boolean) => void
}) {
  const [template, setTemplate] = useState<GoalTemplate | null>(null)

  if (goal === null && template === null) {
    return <TemplatePicker onPick={setTemplate} />
  }
  return (
    <EditorBody
      goal={goal}
      template={template}
      accounts={accounts}
      onBack={goal === null ? () => setTemplate(null) : undefined}
      onDone={onDone}
    />
  )
}

function TemplatePicker({ onPick }: { onPick: (template: GoalTemplate) => void }) {
  return (
    <DialogContent title="Create a goal" description="What are you saving for?">
      <div className="goal-templates">
        {GOAL_TEMPLATES.map((template) => (
          <button
            key={template.key}
            type="button"
            className="goal-template"
            onClick={() => onPick(template)}
          >
            <span className="goal-template__emoji" aria-hidden="true">
              {template.emoji}
            </span>
            <span className="goal-template__label">{template.label}</span>
          </button>
        ))}
      </div>
    </DialogContent>
  )
}

function EditorBody({
  goal,
  template,
  accounts,
  onBack,
  onDone,
}: {
  goal: Goal | null
  template: GoalTemplate | null
  accounts: readonly { id: string; name: string }[]
  onBack?: () => void
  onDone: (open: boolean) => void
}) {
  const [edits, setEdits] = useState<GoalEdits>(() =>
    goal ? editsFromGoal(goal) : blankGoalEdits(template ?? undefined),
  )
  const [amountError, setAmountError] = useState<string | null>(null)
  const [addingAccount, setAddingAccount] = useState(false)
  const { show } = useToast()
  const create = useCreateGoal()
  const update = useUpdateGoal()
  const pending = create.isPending || update.isPending

  const set = (patch: Partial<GoalEdits>) => setEdits((current) => ({ ...current, ...patch }))

  const done = (saved: Goal) => {
    show({ title: goal ? `Saved ${saved.name}` : `Added ${saved.name}`, tone: 'success' })
    onDone(false)
  }

  const submit = () => {
    const body = goalBodyFromEdits(edits)
    if (body === null) {
      setAmountError(`"${edits.target_amount}" is not an amount.`)
      return
    }
    setAmountError(null)
    if (goal) {
      update.mutate({ id: goal.id, body }, { onSuccess: done })
    } else {
      create.mutate(body, { onSuccess: done })
    }
  }

  const named = edits.name.trim() !== ''

  const toggleFunding = (id: string) =>
    set({
      funding_account_ids: edits.funding_account_ids.includes(id)
        ? edits.funding_account_ids.filter((existing) => existing !== id)
        : [...edits.funding_account_ids, id],
    })

  return (
    <DialogContent
      title={
        goal ? (
          'Edit goal'
        ) : (
          <span className="row dialog__title-back">
            <IconButton label="Back to the templates" size="sm" variant="ghost" onClick={onBack}>
              <ArrowLeft size={14} />
            </IconButton>
            New goal
          </span>
        )
      }
      footer={
        <DialogActions onCancel={() => onDone(false)}>
          <Button
            variant="primary"
            onClick={submit}
            disabled={pending || !named || !edits.account_id}
          >
            {goal ? 'Save' : 'Create goal'}
          </Button>
        </DialogActions>
      }
    >
      <div className="form-row">
        <Field label="Name">
          <Input value={edits.name} onChange={(event) => set({ name: event.target.value })} />
        </Field>
        <Field label="Emoji" hint="Optional. The card shows 🎯 when this is blank.">
          <Input
            value={edits.emoji}
            maxLength={8}
            onChange={(event) => set({ emoji: event.target.value })}
          />
        </Field>
      </div>

      <Field
        label="Reserve in account"
        hint={
          accounts.length === 0
            ? 'No accounts yet. Add the one the money sits in.'
            : "What is saved here stops counting toward this account's available balance."
        }
      >
        {accounts.length === 0 ? (
          <Button onClick={() => setAddingAccount(true)}>
            <Plus size={13} aria-hidden="true" /> New account
          </Button>
        ) : (
          <AccountSelect
            accounts={accounts}
            value={edits.account_id}
            onValueChange={(value) => set({ account_id: value })}
          />
        )}
      </Field>
      <NewAccountDialog
        open={addingAccount}
        onOpenChange={setAddingAccount}
        onCreated={(account) => {
          // Only a cash account can hold a reserve; anything else is added
          // but not picked, and the list says why by leaving it out.
          if (account.kind === 'cash') set({ account_id: account.id })
        }}
      />

      {/* The reserve lands in the account above, so it is shown checked and
          fixed. Each account's share is read off the contributions, not typed. */}
      <Field
        label="Funded from"
        as="group"
        hint="Contributions from any of these count toward the goal."
      >
        <div className="option-list">
          {accounts.map((account) => (
            <div key={account.id} className="option-list__row">
              <Checkbox
                label={account.name}
                checked={
                  account.id === edits.account_id ||
                  edits.funding_account_ids.includes(account.id)
                }
                disabled={account.id === edits.account_id}
                onCheckedChange={() => toggleFunding(account.id)}
              />
            </div>
          ))}
        </div>
      </Field>

      <div className="form-row">
        <Field label="Target amount" error={amountError}>
          <MoneyInput
            value={edits.target_amount}
            onChange={(event) => set({ target_amount: event.target.value })}
          />
        </Field>
        <Field label="Target date" hint="Optional. no date means no required monthly rate.">
          <Input
            type="date"
            value={edits.target_on ?? ''}
            onChange={(event) => set({ target_on: event.target.value || null })}
          />
        </Field>
      </div>

      <Switch
        label="Count toward the spending plan"
        checked={edits.is_taken_from_plan}
        onCheckedChange={(checked) => set({ is_taken_from_plan: checked })}
      />
      <p className="muted">
        {edits.is_taken_from_plan
          ? "Contributions to this goal appear in the spending plan's Goals bucket."
          : 'Off: its funding is already counted elsewhere in the plan. On would count it twice.'}
      </p>
    </DialogContent>
  )
}
