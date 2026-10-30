/**
 * What the mail-rule form holds, what the resource is sent, and what a rule
 * reads as in a sentence.
 *
 * A rule posts a transaction or files a bill; either way the same fields are
 * read. A rule reads its amount off a label or a pattern, never both, so the
 * unchosen source (and whatever a bill rule does not use) goes out empty
 * rather than stale.
 *
 * Empty strings, never nulls, for text; only ids carry null.
 */

import type { BillSubaccount } from '@/lib/clients/bills'
import type {
  MailRule,
  MailRuleAction,
  MailRuleDraft,
  MailRuleSuggestion,
} from '@/lib/clients/email'

/** Where the amount comes from: text just before it, or a pattern around it. */
export type AmountSource = 'label' | 'pattern'

/** The same for the date, plus the mail's own arrival — which needs no field. */
export type DateSource = 'label' | 'pattern' | 'received'

/** The payee is typed, or it is the rest of the line after a label. */
export type PayeeSource = 'text' | 'label'

export interface MailRuleForm {
  name: string
  enabled: boolean
  action: MailRuleAction
  sender: string
  subjectContains: string
  bodyContains: string

  amountFrom: AmountSource
  amountLabel: string
  amountPattern: string
  dateFrom: DateSource
  dateLabel: string
  datePattern: string
  referenceLabel: string
  /** Carried rather than edited, so a pattern written elsewhere survives opening the form. */
  referencePattern: string
  /** A bill's statement date, after a label. Optional. */
  issuedLabel: string
  /** Carried rather than edited, like the reference pattern. */
  issuedPattern: string
  /** A card or loan statement's minimum payment, after a label. Optional. */
  minimumLabel: string
  /** Carried rather than edited, like the reference pattern. */
  minimumPattern: string
  payeeFrom: PayeeSource
  payee: string
  payeeLabel: string
  /** The notes keep the lines below this label. Optional; a transaction rule's. */
  notesLabel: string
  /** Up to the line holding this one, or the first blank line when empty. */
  notesEndLabel: string

  accountId: string | null
  categoryId: string | null
  direction: MailRule['direction']
  padIncome: boolean
  incomeAccountId: string | null
  incomeCategoryId: string | null
  incomePayee: string

  /** The bill provider connection a bill rule files on. */
  billConnectionId: string | null
  /** The billed account it pins; null lets the account number choose. */
  billSubaccountId: string | null
  /**
   * The card or loan the billed account should be linked to: null to unlink,
   * undefined to leave it. Set on the billed account on save, not on the rule.
   */
  statementAccountId?: string | null
}

export function emptyMailRuleForm(): MailRuleForm {
  return {
    name: '',
    enabled: true,
    action: 'transaction',
    sender: '',
    subjectContains: '',
    bodyContains: '',
    amountFrom: 'label',
    amountLabel: '',
    amountPattern: '',
    // The mail's own date needs nothing typed and is right for most receipts,
    // so it is where a new rule starts.
    dateFrom: 'received',
    dateLabel: '',
    datePattern: '',
    referenceLabel: '',
    referencePattern: '',
    issuedLabel: '',
    issuedPattern: '',
    minimumLabel: '',
    minimumPattern: '',
    payeeFrom: 'text',
    payee: '',
    payeeLabel: '',
    notesLabel: '',
    notesEndLabel: '',
    accountId: null,
    categoryId: null,
    direction: 'expense',
    padIncome: false,
    incomeAccountId: null,
    incomeCategoryId: null,
    incomePayee: '',
    billConnectionId: null,
    billSubaccountId: null,
  }
}

/** A new rule filing a just-added provider's mailed bills, named for and pointed at it. */
export function billMailRuleForm(connection: { id: string }, name: string): MailRuleForm {
  return {
    ...emptyMailRuleForm(),
    name: `${name} bills`,
    action: 'bill',
    billConnectionId: connection.id,
  }
}

/**
 * A stored rule as the form holds it. Which source was chosen is read back
 * from which field carries text; the wire has no "source" of its own.
 */
export function mailRuleForm(rule: MailRule): MailRuleForm {
  return {
    name: rule.name,
    enabled: rule.enabled,
    action: rule.action,
    sender: rule.sender,
    subjectContains: rule.subject_contains,
    bodyContains: rule.body_contains,
    amountFrom: rule.amount_pattern !== '' ? 'pattern' : 'label',
    amountLabel: rule.amount_label,
    amountPattern: rule.amount_pattern,
    dateFrom:
      rule.date_pattern !== '' ? 'pattern' : rule.date_label !== '' ? 'label' : 'received',
    dateLabel: rule.date_label,
    datePattern: rule.date_pattern,
    referenceLabel: rule.reference_label,
    referencePattern: rule.reference_pattern,
    issuedLabel: rule.issued_label,
    issuedPattern: rule.issued_pattern,
    minimumLabel: rule.minimum_label,
    minimumPattern: rule.minimum_pattern,
    payeeFrom: rule.payee_label !== '' ? 'label' : 'text',
    payee: rule.payee,
    payeeLabel: rule.payee_label,
    notesLabel: rule.notes_label,
    notesEndLabel: rule.notes_end_label,
    accountId: rule.account_id === '' ? null : rule.account_id,
    categoryId: rule.category_id === '' ? null : rule.category_id,
    direction: rule.direction,
    padIncome: rule.pad_income,
    incomeAccountId: rule.income_account_id,
    incomeCategoryId: rule.income_category_id,
    incomePayee: rule.income_payee,
    billConnectionId: rule.bill_connection_id,
    billSubaccountId: rule.bill_subaccount_id,
  }
}

/**
 * A rule the assistant drafted, read the same way as a stored one. Ids the
 * model could not name stay empty for the person to choose.
 */
export function suggestedMailRuleForm(suggestion: MailRuleSuggestion['rule']): MailRuleForm {
  return {
    ...emptyMailRuleForm(),
    action: suggestion.action,
    name: suggestion.name,
    sender: suggestion.sender,
    subjectContains: suggestion.subject_contains,
    bodyContains: suggestion.body_contains,
    amountFrom: suggestion.amount_pattern !== '' && suggestion.amount_label === '' ? 'pattern' : 'label',
    amountLabel: suggestion.amount_label,
    amountPattern: suggestion.amount_pattern,
    dateFrom:
      suggestion.date_label !== ''
        ? 'label'
        : suggestion.date_pattern !== ''
          ? 'pattern'
          : 'received',
    dateLabel: suggestion.date_label,
    datePattern: suggestion.date_pattern,
    referenceLabel: suggestion.reference_label,
    referencePattern: suggestion.reference_pattern,
    issuedLabel: suggestion.issued_label,
    issuedPattern: suggestion.issued_pattern,
    minimumLabel: suggestion.minimum_label,
    minimumPattern: suggestion.minimum_pattern,
    payeeFrom: suggestion.payee === '' && suggestion.payee_label !== '' ? 'label' : 'text',
    payee: suggestion.payee,
    payeeLabel: suggestion.payee_label,
    notesLabel: suggestion.notes_label,
    notesEndLabel: suggestion.notes_end_label,
    accountId: suggestion.account_id,
    categoryId: suggestion.category_id,
    direction: suggestion.direction,
    padIncome: suggestion.pad_income,
    incomeAccountId: suggestion.income_account_id,
    incomeCategoryId: suggestion.income_category_id,
    incomePayee: suggestion.income_payee,
    billConnectionId: suggestion.bill_connection_id,
    // Only a named card is a proposal; nothing named leaves any link alone.
    statementAccountId: suggestion.statement_account_id ?? undefined,
  }
}

/**
 * What the form means, refused before it is sent, as one sentence. The pattern
 * is compiled here as well as on the server to save a round trip.
 */
export function mailRuleProblem(form: MailRuleForm): string | null {
  if (form.name.trim() === '') return 'Give the rule a name.'

  if (form.amountFrom === 'label' && form.amountLabel.trim() === '')
    return 'Say what text comes just before the amount.'
  if (form.amountFrom === 'pattern') {
    if (form.amountPattern.trim() === '') return 'Give a pattern that captures the amount.'
    if (!compiles(form.amountPattern)) return 'That amount pattern is not a valid expression.'
  }

  const date = form.action === 'bill' ? 'due date' : 'date'
  if (form.dateFrom === 'label' && form.dateLabel.trim() === '')
    return `Say what text comes just before the ${date}, or read the day it arrived.`
  if (form.dateFrom === 'pattern') {
    if (form.datePattern.trim() === '') return `Give a pattern that captures the ${date}.`
    if (!compiles(form.datePattern)) return `That ${date} pattern is not a valid expression.`
  }

  if (form.referencePattern !== '' && !compiles(form.referencePattern))
    return form.action === 'bill'
      ? 'That account number pattern is not a valid expression.'
      : 'That reference pattern is not a valid expression.'

  if (form.action === 'bill') {
    if (form.issuedPattern !== '' && !compiles(form.issuedPattern))
      return 'That statement date pattern is not a valid expression.'
    if (form.minimumPattern !== '' && !compiles(form.minimumPattern))
      return 'That minimum payment pattern is not a valid expression.'
    if (form.billConnectionId === null) return 'Choose the bill provider the bill is filed on.'
    return null
  }

  if (form.accountId === null) return 'Choose the account the transaction lands in.'
  if (form.categoryId === null) return 'Choose the category it is filed under.'
  if (form.padIncome && form.incomeCategoryId === null)
    return 'Choose the category the income it was taken from is filed under.'

  return null
}

function compiles(pattern: string): boolean {
  try {
    new RegExp(pattern)
    return true
  } catch {
    return false
  }
}

/**
 * The form as the resource takes it, or null while it is invalid. Every field
 * is sent: the resource reads a present key as an answer, so an empty
 * `amount_pattern` is what switches a rule back to the label.
 */
export function mailRuleDraft(form: MailRuleForm): MailRuleDraft | null {
  if (mailRuleProblem(form) !== null) return null

  const read = {
    name: form.name.trim(),
    enabled: form.enabled,
    sender: form.sender.trim(),
    subject_contains: form.subjectContains.trim(),
    body_contains: form.bodyContains.trim(),
    amount_label: form.amountFrom === 'label' ? form.amountLabel.trim() : '',
    amount_pattern: form.amountFrom === 'pattern' ? form.amountPattern.trim() : '',
    date_label: form.dateFrom === 'label' ? form.dateLabel.trim() : '',
    date_pattern: form.dateFrom === 'pattern' ? form.datePattern.trim() : '',
    reference_label: form.referenceLabel.trim(),
    reference_pattern: form.referencePattern.trim(),
  }

  if (form.action === 'bill') {
    if (form.billConnectionId === null) return null
    return {
      ...read,
      issued_label: form.issuedLabel.trim(),
      issued_pattern: form.issuedPattern.trim(),
      minimum_label: form.minimumLabel.trim(),
      minimum_pattern: form.minimumPattern.trim(),
      payee: '',
      payee_label: '',
      notes_label: '',
      notes_end_label: '',
      action: 'bill',
      account_id: null,
      category_id: null,
      direction: 'expense',
      pad_income: false,
      income_account_id: null,
      income_category_id: null,
      income_payee: '',
      bill_connection_id: form.billConnectionId,
      bill_subaccount_id: form.billSubaccountId,
    }
  }

  if (form.accountId === null || form.categoryId === null) return null
  return {
    ...read,
    issued_label: '',
    issued_pattern: '',
    minimum_label: '',
    minimum_pattern: '',
    payee: form.payeeFrom === 'text' ? form.payee.trim() : '',
    payee_label: form.payeeFrom === 'label' ? form.payeeLabel.trim() : '',
    notes_label: form.notesLabel.trim(),
    // An end with nothing to start from is not sent, so it cannot linger unseen.
    notes_end_label: form.notesLabel.trim() === '' ? '' : form.notesEndLabel.trim(),
    action: 'transaction',
    account_id: form.accountId,
    category_id: form.categoryId,
    direction: form.direction,
    pad_income: form.padIncome,
    income_account_id: form.padIncome ? form.incomeAccountId : null,
    income_category_id: form.padIncome ? form.incomeCategoryId : null,
    income_payee: form.padIncome ? form.incomePayee.trim() : '',
    bill_connection_id: null,
    bill_subaccount_id: null,
  }
}

/**
 * The billed account a bill rule's statement link belongs on: the one the rule
 * pins, or the provider's only one. Null where the account number decides
 * between several, because then there is no one account to link.
 */
export function statementTarget<T extends Pick<BillSubaccount, 'id' | 'connection_id'>>(
  form: Pick<MailRuleForm, 'action' | 'billConnectionId' | 'billSubaccountId'>,
  billed: readonly T[],
): T | null {
  if (form.action !== 'bill' || form.billConnectionId === null) return null
  const theirs = billed.filter((one) => one.connection_id === form.billConnectionId)
  if (form.billSubaccountId !== null)
    return theirs.find((one) => one.id === form.billSubaccountId) ?? null
  return theirs.length === 1 ? theirs[0] : null
}

/**
 * The link to send once the rule is saved: the account to set, null to clear
 * it, or undefined when the form leaves the link as it stands.
 */
export function statementLinkChange(
  form: Pick<MailRuleForm, 'statementAccountId'>,
  target: Pick<BillSubaccount, 'account_id'> | null,
): string | null | undefined {
  if (target === null || form.statementAccountId === undefined) return undefined
  return form.statementAccountId === target.account_id ? undefined : form.statementAccountId
}

/** Which mail a rule claims, as a sentence. */
export function describeMailRuleMatch(
  rule: Pick<MailRule, 'sender' | 'subject_contains' | 'body_contains'>,
): string {
  const parts: string[] = []
  if (rule.sender !== '')
    parts.push(
      rule.sender.startsWith('@')
        ? `from anybody at ${rule.sender.slice(1)}`
        : `from ${rule.sender}`,
    )
  if (rule.subject_contains !== '') parts.push(`with “${rule.subject_contains}” in the subject`)
  if (rule.body_contains !== '') parts.push(`with “${rule.body_contains}” in the body`)

  if (parts.length === 0) return 'Any mail that arrives'
  return `Mail ${parts.join(', ')}`
}

/** The names a summary needs, looked up wherever the screen holds them. */
export interface MailRuleNames {
  account: (id: string | null) => string | undefined
  category: (id: string | null) => string | undefined
  /** A bill provider connection, as its heading names it. */
  provider: (id: string | null) => string | undefined
  /** A billed account under one. */
  billedAccount: (id: string | null) => string | undefined
}

/**
 * What a rule posts, as a sentence. A name not loaded yet is left out rather
 * than shown as "Unknown account".
 */
export function describeMailRuleAction(
  rule: Pick<
    MailRule,
    | 'action'
    | 'direction'
    | 'account_id'
    | 'category_id'
    | 'pad_income'
    | 'income_category_id'
    | 'bill_connection_id'
    | 'bill_subaccount_id'
  >,
  names: MailRuleNames,
): string {
  if (rule.action === 'bill') {
    const provider = names.provider(rule.bill_connection_id)
    const billed = names.billedAccount(rule.bill_subaccount_id)
    if (provider === undefined) return 'Files a bill'
    return billed === undefined
      ? `Files a bill on ${provider}`
      : `Files a bill on ${provider} · ${billed}`
  }

  const account = names.account(rule.account_id)
  const category = names.category(rule.category_id)

  let sentence = rule.direction === 'income' ? 'Posts income' : 'Posts an expense'
  if (account !== undefined) sentence += ` to ${account}`
  if (category !== undefined) sentence += ` as ${category}`

  if (!rule.pad_income) return sentence

  const padded = names.category(rule.income_category_id)
  return padded === undefined ? `${sentence}; pads income` : `${sentence}; pads income as ${padded}`
}
