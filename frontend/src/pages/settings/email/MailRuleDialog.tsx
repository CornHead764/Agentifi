import { ChevronDown, ChevronRight, Plus } from 'lucide-react'
import { useState } from 'react'

import { useMoneyText } from '@/components/moneyText'
import { AccountPicker, CategoryPicker } from '@/components/transactions/Pickers'
import {
  Button,
  Checkbox,
  ChipGroup,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  Input,
  OptionSelect,
  Textarea,
} from '@/components/ui'
import { AccountSelect } from '@/components/AccountSelect'
import { maskedNumber } from '@/lib/accounts'
import { connectionTitle } from '@/lib/billers'
import { findCategory } from '@/lib/categoryNames'
import {
  useAllBillSubaccounts,
  useBillConnections,
  useUpdateBillSubaccount,
} from '@/lib/clients/bills'
import {
  useCreateMailRule,
  useTryMailRule,
  useUpdateMailRule,
  type MailRule,
  type MailRuleAction,
  type MailRuleDirection,
  type MailSample,
} from '@/lib/clients/email'
import { formatDate } from '@/lib/format'
import { useAccounts, useCategories } from '@/lib/transactions/queries'

import { ConnectionDialog } from '../bills/ConnectionDialog'
import {
  emptyMailRuleForm,
  mailRuleDraft,
  mailRuleForm,
  mailRuleProblem,
  statementLinkChange,
  statementTarget,
  type AmountSource,
  type DateSource,
  type MailRuleForm,
  type PayeeSource,
} from './mailRule'

/** The provider for a company with no connector, whose bills arrive by mail. */
const EMAIL_ONLY = 'email-only'

/**
 * Write a rule for the mail no built-in parser knows, or correct the one there.
 * What the rule does comes first, because a bill changes the rest of the form:
 * the date is the due date, the reference is the account number, and it lands
 * on a bill provider rather than an account.
 *
 * **Try it sends the draft, never saves it.** The server answers what the rule
 * would read and post, and writes nothing.
 */
export function MailRuleDialog({
  rule,
  initial,
  sample,
  notes = [],
  onClose,
}: {
  /** The rule being edited, or null to write one. */
  rule: MailRule | null
  /** A new rule's starting point — the assistant's draft — instead of empty. */
  initial?: MailRuleForm
  /** The mail the draft was made from, already in the Try it box. */
  sample?: MailSample
  /** Sentences about the draft worth reading before saving it. */
  notes?: readonly string[]
  onClose: () => void
}) {
  const [form, setForm] = useState<MailRuleForm>(
    () => initial ?? (rule === null ? emptyMailRuleForm() : mailRuleForm(rule)),
  )
  // Whether anything has been touched, which is the only thing standing
  // between an empty new rule and a dialog that opens already complaining.
  const [dirty, setDirty] = useState(rule !== null || initial !== undefined)
  const [showingTry, setShowingTry] = useState(sample !== undefined)
  const [addingCompany, setAddingCompany] = useState(false)
  const [sender, setSender] = useState(sample?.sender ?? '')
  const [subject, setSubject] = useState(sample?.subject ?? '')
  const [text, setText] = useState(sample?.text ?? '')

  const accounts = useAccounts()
  const categories = useCategories()
  const providers = useBillConnections()
  const billed = useAllBillSubaccounts()
  const create = useCreateMailRule()
  const update = useUpdateMailRule()
  const link = useUpdateBillSubaccount()
  const trial = useTryMailRule()
  const moneyText = useMoneyText()
  const pending = create.isPending || update.isPending

  const set = (patch: Partial<MailRuleForm>) => {
    setDirty(true)
    setForm({ ...form, ...patch })
  }

  const problem = mailRuleProblem(form)
  const draft = mailRuleDraft(form)

  const accountRows = accounts.data ?? []
  const categoryRows = categories.data ?? []
  const accountName = (id: string | null) =>
    id === null ? undefined : accountRows.find((one) => one.id === id)?.name
  const categoryName = (id: string | null) => findCategory(categoryRows, id)?.name
  const providerRows = providers.data ?? []
  const providerName = (id: string | null) => {
    const found = id === null ? undefined : providerRows.find((one) => one.id === id)
    return found === undefined ? undefined : connectionTitle(found)
  }
  // Only the chosen provider's billed accounts: one at another provider is
  // refused on save, so it is never offered.
  const billedRows = (billed.data ?? []).filter(
    (one) => one.connection_id === form.billConnectionId,
  )
  const bill = form.action === 'bill'
  // Only where there is one account to link: a rule that lets the account
  // number choose among several has none.
  const target = statementTarget(form, billed.data ?? [])
  const statementAccounts = accountRows.filter(
    (one) => one.kind === 'credit_card' || one.kind === 'loan',
  )
  const statementOf =
    form.statementAccountId === undefined ? (target?.account_id ?? null) : form.statementAccountId

  const save = () => {
    if (draft === null) return
    const change = statementLinkChange(form, target)
    const done = () => {
      if (target !== null && change !== undefined)
        link.mutate({ id: target.id, patch: { account_id: change } }, { onSuccess: onClose })
      else onClose()
    }
    if (rule) update.mutate({ id: rule.id, patch: draft }, { onSuccess: done })
    else create.mutate(draft, { onSuccess: done })
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent
        title={rule ? `Edit ${rule.name}` : 'Add a mail rule'}
        description="For mail no built-in parser reads: which messages, what to read, and whether to post a transaction or file a bill."
        onSubmit={(event) => {
          event.preventDefault()
          save()
        }}
        footer={
          <DialogActions>
            <Button type="submit" variant="primary" disabled={draft === null || pending}>
              {rule ? 'Save' : 'Add rule'}
            </Button>
          </DialogActions>
        }
      >
        {initial ? (
          <div className="mailrule-suggested" role="note">
            <p>
              Drafted by the assistant; not saved until you add it. Check each field and use Try
              it below.
            </p>
            {notes.length > 0 ? (
              <ul>
                {notes.map((note) => (
                  <li key={note}>{note}</li>
                ))}
              </ul>
            ) : null}
          </div>
        ) : null}

        <Field label="Name" hint="What to call this rule in the list.">
          <Input
            value={form.name}
            onChange={(event) => set({ name: event.target.value })}
            placeholder={bill ? 'Water bill' : 'Lunch receipts'}
            maxLength={80}
          />
        </Field>

        <Field
          as="group"
          label="This rule"
          hint={
            bill
              ? 'Files a mailed bill on a provider; nothing is posted. The payment pairs with it on the bank sync.'
              : 'Posts a receipt as a transaction.'
          }
        >
          <ChipGroup
            label="What the rule does"
            value={form.action}
            options={ACTIONS}
            onChange={(next) => set({ action: asAction(next) })}
          />
        </Field>

        <h3>When a mail</h3>

        <Field
          label="From"
          hint="One address, or “@” and a domain for anybody there. Empty means any sender."
        >
          <Input
            value={form.sender}
            onChange={(event) => set({ sender: event.target.value })}
            placeholder="@lunch.example"
            autoComplete="off"
          />
        </Field>

        <div className="form-row">
          <Field label="Subject contains" hint="Left empty, the subject is not looked at.">
            <Input
              value={form.subjectContains}
              onChange={(event) => set({ subjectContains: event.target.value })}
              placeholder="Receipt"
            />
          </Field>
          <Field label="Body contains" hint="One line that every one of these mails carries.">
            <Input
              value={form.bodyContains}
              onChange={(event) => set({ bodyContains: event.target.value })}
            />
          </Field>
        </div>

        <h3>Read</h3>

        <Field
          as="group"
          label={bill ? 'Amount due' : 'Amount'}
          hint={
            form.amountFrom === 'label'
              ? bill
                ? 'The text just before the figure, on the same line — “Amount due:”.'
                : 'The text just before the figure, on the same line — “Receipt Total:”.'
              : 'An expression with one capture group around the figure.'
          }
        >
          <ChipGroup
            label="Where the amount comes from"
            value={form.amountFrom}
            options={AMOUNT_SOURCES}
            onChange={(next) => set({ amountFrom: asAmountSource(next) })}
          />
          {form.amountFrom === 'label' ? (
            <Input
              value={form.amountLabel}
              onChange={(event) => set({ amountLabel: event.target.value })}
              placeholder={bill ? 'Amount due:' : 'Receipt Total:'}
              aria-label="Text before the amount"
            />
          ) : (
            <Input
              value={form.amountPattern}
              onChange={(event) => set({ amountPattern: event.target.value })}
              placeholder="Total\s+\$([0-9.,]+)"
              aria-label="Amount pattern"
              autoComplete="off"
            />
          )}
        </Field>

        <Field
          as="group"
          label={bill ? 'Due date' : 'Date'}
          hint={
            form.dateFrom === 'received'
              ? bill
                ? 'The day the mail arrived. Only right for a bill that is its own payment; otherwise read the due date.'
                : 'The day the mail arrived, for a receipt with no date of its own.'
              : form.dateFrom === 'label'
                ? bill
                  ? 'The text just before the due date — “Due date:”.'
                  : 'The text just before the date — “Receipt Date:”.'
                : 'An expression with one capture group around the date.'
          }
        >
          <ChipGroup
            label="Where the date comes from"
            value={form.dateFrom}
            options={DATE_SOURCES}
            onChange={(next) => set({ dateFrom: asDateSource(next) })}
          />
          {form.dateFrom === 'label' ? (
            <Input
              value={form.dateLabel}
              onChange={(event) => set({ dateLabel: event.target.value })}
              placeholder={bill ? 'Due date:' : 'Receipt Date:'}
              aria-label="Text before the date"
            />
          ) : null}
          {form.dateFrom === 'pattern' ? (
            <Input
              value={form.datePattern}
              onChange={(event) => set({ datePattern: event.target.value })}
              aria-label="Date pattern"
              autoComplete="off"
            />
          ) : null}
        </Field>

        {bill ? (
          <div className="form-row">
            <Field
              label="Account number label"
              hint="Optional. Picks the billed account when there are several."
            >
              <Input
                value={form.referenceLabel}
                onChange={(event) => set({ referenceLabel: event.target.value })}
                placeholder="Account number:"
              />
            </Field>
            <Field
              label="Statement date label"
              hint="Optional."
            >
              <Input
                value={form.issuedLabel}
                onChange={(event) => set({ issuedLabel: event.target.value })}
                placeholder="Statement date:"
              />
            </Field>
            <Field
              label="Minimum payment label"
              hint="Optional, for a card or loan statement."
            >
              <Input
                value={form.minimumLabel}
                onChange={(event) => set({ minimumLabel: event.target.value })}
                placeholder="Minimum Payment Due:"
              />
            </Field>
          </div>
        ) : (
          <Field
            label="Reference label"
            hint="Optional. The receipt's number, kept on the transaction."
          >
            <Input
              value={form.referenceLabel}
              onChange={(event) => set({ referenceLabel: event.target.value })}
              placeholder="ReceiptID:"
            />
          </Field>
        )}

        {bill ? null : (
          <Field
            as="group"
            label="Payee"
            hint={
              form.payeeFrom === 'text'
                ? 'The same name on every row this rule posts.'
                : 'The rest of the line after this label, e.g. “Your receipt from”.'
            }
          >
            <ChipGroup
              label="Where the payee comes from"
              value={form.payeeFrom}
              options={PAYEE_SOURCES}
              onChange={(next) => set({ payeeFrom: asPayeeSource(next) })}
            />
            {form.payeeFrom === 'text' ? (
              <Input
                value={form.payee}
                onChange={(event) => set({ payee: event.target.value })}
                aria-label="Payee"
              />
            ) : (
              <Input
                value={form.payeeLabel}
                onChange={(event) => set({ payeeLabel: event.target.value })}
                placeholder="Your receipt from"
                aria-label="Text before the payee"
              />
            )}
          </Field>
        )}

        {bill ? null : (
          <div className="form-row">
            <Field
              label="Notes from below"
              hint="Optional. The lines under this label go in the notes, after the reference — the list of what was bought."
            >
              <Input
                value={form.notesLabel}
                onChange={(event) => set({ notesLabel: event.target.value })}
                placeholder="Item"
              />
            </Field>
            <Field
              label="Notes up to"
              hint="The line holding this label ends them. Left empty, the first blank line does."
            >
              <Input
                value={form.notesEndLabel}
                onChange={(event) => set({ notesEndLabel: event.target.value })}
                placeholder="Subtotal"
                disabled={form.notesLabel.trim() === ''}
              />
            </Field>
          </div>
        )}

        <h3>Then</h3>

        {bill ? (
          <>
            <div className="form-row">
              <Field label="File it on">
                <OptionSelect
                  value={form.billConnectionId ?? ''}
                  onValueChange={(id) => set({ billConnectionId: id, billSubaccountId: null })}
                  placeholder="Choose a bill provider"
                  options={providerRows.map((one) => ({ value: one.id, label: connectionTitle(one) }))}
                />
              </Field>
              <Field
                label="Billed account"
                hint="Default: whichever the account number names."
              >
                <OptionSelect
                  value={form.billSubaccountId ?? BY_ACCOUNT_NUMBER}
                  onValueChange={(id) =>
                    set({ billSubaccountId: id === BY_ACCOUNT_NUMBER ? null : id })
                  }
                  disabled={form.billConnectionId === null}
                  options={[
                    { value: BY_ACCOUNT_NUMBER, label: 'Whichever the account number names' },
                    ...billedRows.map((one) => ({
                      value: one.id,
                      label: one.masked_number
                        ? `${one.label} · ${maskedNumber(one.masked_number)}`
                        : one.label,
                    })),
                  ]}
                />
              </Field>
            </div>
            {target === null ? null : (
              <Field
                label="Statement of"
                hint="Card or loan whose statement figures each bill fills. A figure you type for the same cycle stays."
              >
                <AccountSelect
                  accounts={statementAccounts}
                  value={statementOf ?? NO_STATEMENT_ACCOUNT}
                  onValueChange={(id) =>
                    set({ statementAccountId: id === NO_STATEMENT_ACCOUNT ? null : id })
                  }
                  firstOption={{ value: NO_STATEMENT_ACCOUNT, label: 'No account' }}
                  aria-label="Account this statement is of"
                />
              </Field>
            )}
            <p className="hint">
              Not listed? Add the company; with no PDF, the mail is kept as the statement.
            </p>
            <Button size="sm" variant="ghost" onClick={() => setAddingCompany(true)}>
              <Plus size={13} aria-hidden="true" /> Add a company
            </Button>
            {addingCompany ? (
              <ConnectionDialog
                connection={null}
                preset={EMAIL_ONLY}
                onClose={() => setAddingCompany(false)}
                onCreated={(created) =>
                  set({ billConnectionId: created.id, billSubaccountId: null })
                }
              />
            ) : null}
          </>
        ) : (
          <>
            <div className="form-row">
              <Field label="Account">
                <AccountPicker
                  value={form.accountId ?? ''}
                  accounts={accountRows}
                  onChange={(id) => set({ accountId: id })}
                  trigger={
                    <button type="button" className="select__trigger">
                      {accountName(form.accountId) ?? 'Choose an account'}
                    </button>
                  }
                />
              </Field>
              <Field label="Category">
                <CategoryPicker
                  value={form.categoryId}
                  categories={categoryRows}
                  frequentIds={[]}
                  onChange={(id) => set({ categoryId: id })}
                  trigger={
                    <button type="button" className="select__trigger">
                      {categoryName(form.categoryId) ?? 'Choose a category'}
                    </button>
                  }
                />
              </Field>
            </div>

            <Field as="group" label="Direction">
              <ChipGroup
                label="Which way the money goes"
                value={form.direction}
                options={DIRECTIONS}
                onChange={(next) => set({ direction: asDirection(next) })}
              />
            </Field>

            <Checkbox
              label="Also record the income it was taken from"
              checked={form.padIncome}
              onCheckedChange={(checked) => set({ padIncome: checked === true })}
            />

            {form.padIncome ? (
              <>
                <p className="hint">
                  For a paycheck deduction: the deposit is already net of it, so the same figure
                  is added back as income.
                </p>
                <div className="form-row">
                  <Field label="Income category">
                    <CategoryPicker
                      value={form.incomeCategoryId}
                      categories={categoryRows}
                      frequentIds={[]}
                      onChange={(id) => set({ incomeCategoryId: id })}
                      trigger={
                        <button type="button" className="select__trigger">
                          {categoryName(form.incomeCategoryId) ?? 'Choose a category'}
                        </button>
                      }
                    />
                  </Field>
                  <Field label="Income account" hint="The same account, unless you name another.">
                    <AccountPicker
                      value={form.incomeAccountId ?? ''}
                      accounts={accountRows}
                      onChange={(id) => set({ incomeAccountId: id })}
                      trigger={
                        <button type="button" className="select__trigger">
                          {accountName(form.incomeAccountId) ?? 'The same account'}
                        </button>
                      }
                    />
                  </Field>
                </div>
                {form.incomeAccountId === null ? null : (
                  <Button size="sm" variant="ghost" onClick={() => set({ incomeAccountId: null })}>
                    Use the same account
                  </Button>
                )}
                <Field
                  label="Income payee"
                  hint="Blank: the payee above plus “(paycheck deduction)”."
                >
                  <Input
                    value={form.incomePayee}
                    onChange={(event) => set({ incomePayee: event.target.value })}
                  />
                </Field>
              </>
            ) : null}
          </>
        )}

        <Button
          size="sm"
          variant="ghost"
          aria-expanded={showingTry}
          onClick={() => setShowingTry(!showingTry)}
        >
          {showingTry ? (
            <ChevronDown size={14} aria-hidden="true" />
          ) : (
            <ChevronRight size={14} aria-hidden="true" />
          )}{' '}
          Try it
        </Button>

        {showingTry ? (
          <>
            <p className="hint">
              Paste a message to see what the rule would read and post or file. Nothing is saved.
            </p>
            <div className="form-row">
              <Field label="From">
                <Input
                  value={sender}
                  onChange={(event) => setSender(event.target.value)}
                  autoComplete="off"
                />
              </Field>
              <Field label="Subject">
                <Input value={subject} onChange={(event) => setSubject(event.target.value)} />
              </Field>
            </div>
            <Field label="Body">
              <Textarea rows={5} value={text} onChange={(event) => setText(event.target.value)} />
            </Field>
            <Button
              size="sm"
              variant="secondary"
              disabled={draft === null || trial.isPending}
              onClick={() => {
                if (draft === null) return
                trial.mutate({ rule: draft, sample: { sender, subject, text } })
              }}
            >
              {trial.isPending ? 'Trying…' : 'Try it'}
            </Button>

            {trial.data ? (
              <div className="mailrule-trial">
                <p>
                  <strong>{trial.data.matched ? 'This rule claims it' : 'No match'}</strong>
                  {trial.data.error === '' ? '' : ` · ${trial.data.error}`}
                </p>
                {trial.data.matched && trial.data.would_file ? (
                  <ul>
                    <li>
                      Amount due:{' '}
                      {trial.data.amount === null ? 'nothing read' : moneyText(trial.data.amount)}
                    </li>
                    <li>
                      Due date:{' '}
                      {trial.data.date === null ? 'nothing read' : formatDate(trial.data.date)}
                    </li>
                    <li>
                      Statement date:{' '}
                      {trial.data.issued_on === null ? 'nothing read' : formatDate(trial.data.issued_on)}
                    </li>
                    {form.minimumLabel !== '' || form.minimumPattern !== '' ? (
                      <li>
                        Minimum payment:{' '}
                        {trial.data.minimum_due === null
                          ? 'nothing read'
                          : moneyText(trial.data.minimum_due)}
                      </li>
                    ) : null}
                    <li>Account number: {trial.data.account || 'nothing read'}</li>
                    {trial.data.error === '' && providerName(form.billConnectionId) !== undefined ? (
                      <li>Would file a bill on {providerName(form.billConnectionId)}</li>
                    ) : null}
                  </ul>
                ) : trial.data.matched ? (
                  <ul>
                    <li>
                      Amount:{' '}
                      {trial.data.amount === null ? 'nothing read' : moneyText(trial.data.amount)}
                    </li>
                    <li>
                      Date:{' '}
                      {trial.data.date === null ? 'nothing read' : formatDate(trial.data.date)}
                    </li>
                    <li>Reference: {trial.data.reference || 'nothing read'}</li>
                    <li>Payee: {trial.data.payee || 'nothing read'}</li>
                    <li>
                      Notes:{' '}
                      {trial.data.notes === '' ? (
                        'nothing read'
                      ) : (
                        <span className="mailrule-trial__notes">{trial.data.notes}</span>
                      )}
                    </li>
                  </ul>
                ) : null}
                {trial.data.would_post.length > 0 ? (
                  <ul>
                    {trial.data.would_post.map((row, index) => (
                      <li key={index}>
                        {moneyText(row.amount)} · {row.payee} ·{' '}
                        {categoryName(row.category_id) ?? 'a category'} in{' '}
                        {accountName(row.account_id) ?? 'an account'}
                      </li>
                    ))}
                  </ul>
                ) : null}
              </div>
            ) : null}
          </>
        ) : null}

        {problem === null || !dirty ? null : <p className="field__error">{problem}</p>}
      </DialogContent>
    </Dialog>
  )
}

const ACTIONS = [
  { value: 'transaction', label: 'Post a transaction' },
  { value: 'bill', label: 'File a bill' },
] as const

/** A sentinel, because a select item cannot carry an empty value. */
const BY_ACCOUNT_NUMBER = 'by-account-number'

/** The statement picker's value for "no account"; a select item cannot carry an empty value. */
const NO_STATEMENT_ACCOUNT = '__none__'

const AMOUNT_SOURCES = [
  { value: 'label', label: 'after the label' },
  { value: 'pattern', label: 'matching a pattern' },
] as const

const DATE_SOURCES = [
  { value: 'label', label: 'after the label' },
  { value: 'pattern', label: 'matching a pattern' },
  { value: 'received', label: 'the day it arrived' },
] as const

const PAYEE_SOURCES = [
  { value: 'text', label: 'this text' },
  { value: 'label', label: 'the rest of the line after' },
] as const

const DIRECTIONS = [
  { value: 'expense', label: 'an expense' },
  { value: 'income', label: 'income' },
] as const

function asAction(value: string): MailRuleAction {
  return ACTIONS.find((one) => one.value === value)?.value ?? 'transaction'
}

function asAmountSource(value: string): AmountSource {
  return AMOUNT_SOURCES.find((one) => one.value === value)?.value ?? 'label'
}

function asDateSource(value: string): DateSource {
  return DATE_SOURCES.find((one) => one.value === value)?.value ?? 'received'
}

function asPayeeSource(value: string): PayeeSource {
  return PAYEE_SOURCES.find((one) => one.value === value)?.value ?? 'text'
}

function asDirection(value: string): MailRuleDirection {
  return DIRECTIONS.find((one) => one.value === value)?.value ?? 'expense'
}
