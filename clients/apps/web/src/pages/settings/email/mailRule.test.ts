import { describe, expect, it } from 'vitest'

import type { MailRule, MailRuleSuggestion } from '@/lib/clients/email'

import {
  billMailRuleForm,
  describeMailRuleAction,
  describeMailRuleMatch,
  emptyMailRuleForm,
  mailRuleDraft,
  mailRuleForm,
  mailRuleProblem,
  statementLinkChange,
  statementTarget,
  suggestedMailRuleForm,
  type MailRuleForm,
  type MailRuleNames,
} from './mailRule'

/** The worked example, invented end to end. */
function form(over: Partial<MailRuleForm> = {}): MailRuleForm {
  return {
    ...emptyMailRuleForm(),
    name: 'Lunch receipts',
    sender: '@lunch.example',
    subjectContains: 'Lunch Receipt',
    amountFrom: 'label',
    amountLabel: 'Receipt Total:',
    dateFrom: 'label',
    dateLabel: 'Receipt Date:',
    referenceLabel: 'ReceiptID:',
    payeeFrom: 'label',
    payeeLabel: 'Your receipt from',
    accountId: 'acct-checking',
    categoryId: 'cat-dining',
    ...over,
  }
}

function rule(over: Partial<MailRule> = {}): MailRule {
  return {
    id: 'rule-1',
    name: 'Lunch receipts',
    enabled: true,
    sender: '@lunch.example',
    subject_contains: 'Lunch Receipt',
    body_contains: '',
    amount_label: 'Receipt Total:',
    amount_pattern: '',
    date_label: 'Receipt Date:',
    date_pattern: '',
    reference_label: 'ReceiptID:',
    reference_pattern: '',
    issued_label: '',
    issued_pattern: '',
    minimum_label: '',
    minimum_pattern: '',
    payee: '',
    payee_label: 'Your receipt from',
    notes_label: '',
    notes_end_label: '',
    action: 'transaction',
    account_id: 'acct-checking',
    category_id: 'cat-dining',
    direction: 'expense',
    pad_income: false,
    income_account_id: null,
    income_category_id: null,
    income_payee: '',
    bill_connection_id: null,
    bill_subaccount_id: null,
    sort_order: 0,
    created_at: '2026-09-01T00:00:00Z',
    ...over,
  }
}

const names: MailRuleNames = {
  account: (id) => (id === 'acct-checking' ? 'Everyday Checking' : undefined),
  category: (id) =>
    id === 'cat-dining' ? 'Dining' : id === 'cat-pay' ? 'Paycheck' : undefined,
  provider: (id) => (id === 'conn-water' ? 'Example Water' : undefined),
  billedAccount: (id) => (id === 'sub-house' ? 'the house' : undefined),
}

describe('the form as the resource takes it', () => {
  it('sends every field, and the source that was not chosen as empty', () => {
    // The resource reads a present key as an answer, so a rule changed from a
    // pattern to a label has to say the pattern is gone.
    const draft = mailRuleDraft(form({ amountPattern: 'Total\\s+\\$([0-9.]+)' }))

    expect(draft).toMatchObject({
      name: 'Lunch receipts',
      sender: '@lunch.example',
      subject_contains: 'Lunch Receipt',
      body_contains: '',
      amount_label: 'Receipt Total:',
      amount_pattern: '',
      date_label: 'Receipt Date:',
      date_pattern: '',
      reference_label: 'ReceiptID:',
      payee: '',
      payee_label: 'Your receipt from',
      action: 'transaction',
      account_id: 'acct-checking',
      category_id: 'cat-dining',
      direction: 'expense',
    })
  })

  it('carries a null only for an id it does not use', () => {
    const draft = mailRuleDraft(form())
    const nulls = Object.entries(draft ?? {}).filter(([, value]) => value === null)

    expect(nulls.map(([field]) => field).sort()).toEqual([
      'bill_connection_id',
      'bill_subaccount_id',
      'income_account_id',
      'income_category_id',
    ])
  })

  it('keeps the padding income where it was told to put it', () => {
    const draft = mailRuleDraft(
      form({
        padIncome: true,
        incomeCategoryId: 'cat-pay',
        incomePayee: 'Lunch deduction',
      }),
    )

    expect(draft).toMatchObject({
      pad_income: true,
      income_category_id: 'cat-pay',
      // Null is a real answer: the income lands in the same account as the
      // expense unless somebody names another.
      income_account_id: null,
      income_payee: 'Lunch deduction',
    })
  })

  it('drops the income fields of a rule that no longer pads', () => {
    const draft = mailRuleDraft(
      form({ padIncome: false, incomeCategoryId: 'cat-pay', incomePayee: 'Lunch deduction' }),
    )

    expect(draft).toMatchObject({
      pad_income: false,
      income_category_id: null,
      income_payee: '',
    })
  })

  it('sends the labels around the notes, and never an end without a start', () => {
    expect(mailRuleDraft(form({ notesLabel: ' Item ', notesEndLabel: 'Subtotal' }))).toMatchObject({
      notes_label: 'Item',
      notes_end_label: 'Subtotal',
    })
    expect(mailRuleDraft(form({ notesLabel: '', notesEndLabel: 'Subtotal' }))).toMatchObject({
      notes_label: '',
      notes_end_label: '',
    })
  })

  it('builds nothing at all while the form means nothing valid', () => {
    expect(mailRuleDraft(form({ accountId: null }))).toBeNull()
  })
})

describe('a stored rule read back into the form', () => {
  it('picks the source that carries text, because nothing else records it', () => {
    expect(mailRuleForm(rule())).toMatchObject({
      amountFrom: 'label',
      dateFrom: 'label',
      payeeFrom: 'label',
    })
    expect(
      mailRuleForm(rule({ amount_label: '', amount_pattern: 'Total\\s+\\$([0-9.]+)' })).amountFrom,
    ).toBe('pattern')
    expect(mailRuleForm(rule({ date_label: '' })).dateFrom).toBe('received')
    expect(mailRuleForm(rule({ payee_label: '', payee: 'The Lunch Program' })).payeeFrom).toBe('text')
  })

  it('survives the round trip back to the wire', () => {
    expect(
      mailRuleDraft(mailRuleForm(rule({ notes_label: 'Item', notes_end_label: 'Subtotal' }))),
    ).toMatchObject({
      amount_label: 'Receipt Total:',
      amount_pattern: '',
      date_label: 'Receipt Date:',
      payee_label: 'Your receipt from',
      notes_label: 'Item',
      notes_end_label: 'Subtotal',
    })
  })
})

describe('what the dialog refuses to send', () => {
  it('takes a rule with an amount source, an account and a category', () => {
    expect(mailRuleProblem(form())).toBeNull()
  })

  it('insists on somewhere to read the amount from', () => {
    expect(mailRuleProblem(form({ amountLabel: '' }))).toContain('before the amount')
    expect(mailRuleProblem(form({ amountFrom: 'pattern', amountPattern: '' }))).toContain(
      'captures the amount',
    )
  })

  it('refuses a pattern that is not one, rather than spending a round trip on it', () => {
    expect(mailRuleProblem(form({ amountFrom: 'pattern', amountPattern: 'Total ([0-9' }))).toContain(
      'not a valid expression',
    )
  })

  it('asks for the date label it is about to read nothing after', () => {
    expect(mailRuleProblem(form({ dateFrom: 'label', dateLabel: '' }))).toContain('before the date')
    // The day the mail arrived needs no field, so it is never a problem.
    expect(mailRuleProblem(form({ dateFrom: 'received', dateLabel: '' }))).toBeNull()
  })

  it('will not post to nowhere', () => {
    expect(mailRuleProblem(form({ name: '' }))).toContain('name')
    expect(mailRuleProblem(form({ accountId: null }))).toContain('account')
    expect(mailRuleProblem(form({ categoryId: null }))).toContain('category')
  })

  it('will not pad income into no category', () => {
    expect(mailRuleProblem(form({ padIncome: true, incomeCategoryId: null }))).toContain(
      'income it was taken from',
    )
    expect(
      mailRuleProblem(form({ padIncome: true, incomeCategoryId: 'cat-pay' })),
    ).toBeNull()
  })
})

describe('a rule in words', () => {
  it('says which mail it claims', () => {
    expect(describeMailRuleMatch(rule())).toBe(
      'Mail from anybody at lunch.example, with “Lunch Receipt” in the subject',
    )
    expect(describeMailRuleMatch(rule({ sender: 'receipts@lunch.example' }))).toContain(
      'Mail from receipts@lunch.example',
    )
    expect(
      describeMailRuleMatch(rule({ subject_contains: '', body_contains: 'Receipt Total' })),
    ).toContain('with “Receipt Total” in the body')
  })

  it('says so plainly when a rule narrows nothing', () => {
    expect(describeMailRuleMatch(rule({ sender: '', subject_contains: '' }))).toBe(
      'Any mail that arrives',
    )
  })

  it('says what it posts, and that the income goes back in', () => {
    expect(describeMailRuleAction(rule(), names)).toBe(
      'Posts an expense to Everyday Checking as Dining',
    )
    expect(
      describeMailRuleAction(
        rule({ pad_income: true, income_category_id: 'cat-pay' }),
        names,
      ),
    ).toBe('Posts an expense to Everyday Checking as Dining; pads income as Paycheck')
    expect(describeMailRuleAction(rule({ direction: 'income' }), names)).toContain('Posts income')
  })

  it('leaves out a name nothing has loaded rather than guessing at one', () => {
    // "Posts an expense to Unknown account" reads as an account that has gone
    // missing; the shorter sentence is simply true.
    const nothing: MailRuleNames = {
      account: () => undefined,
      category: () => undefined,
      provider: () => undefined,
      billedAccount: () => undefined,
    }

    expect(describeMailRuleAction(rule({ pad_income: true }), nothing)).toBe(
      'Posts an expense; pads income',
    )
  })
})

describe('a rule the assistant drafted', () => {
  function suggestion(
    over: Partial<MailRuleSuggestion['rule']> = {},
  ): MailRuleSuggestion['rule'] {
    return {
      action: 'transaction',
      bill_connection_id: null,
      issued_label: '',
      issued_pattern: '',
      minimum_label: '',
      minimum_pattern: '',
      statement_account_id: null,
      name: 'Lunch receipts',
      sender: '@lunch.example',
      subject_contains: 'Lunch Receipt',
      body_contains: '',
      amount_label: 'Receipt Total:',
      amount_pattern: '',
      date_label: '',
      date_pattern: '',
      reference_label: 'ReceiptID:',
      reference_pattern: '',
      payee: 'Lunch',
      payee_label: '',
      notes_label: '',
      notes_end_label: '',
      direction: 'expense',
      account_id: null,
      category_id: null,
      pad_income: false,
      income_account_id: null,
      income_category_id: null,
      income_payee: '',
      ...over,
    }
  }

  it('opens the form with what was drafted, and the day it arrived as the date', () => {
    const drafted = suggestedMailRuleForm(suggestion())
    expect(drafted.name).toBe('Lunch receipts')
    expect(drafted.sender).toBe('@lunch.example')
    expect(drafted.amountFrom).toBe('label')
    expect(drafted.amountLabel).toBe('Receipt Total:')
    expect(drafted.dateFrom).toBe('received')
    expect(drafted.payeeFrom).toBe('text')
    expect(drafted.payee).toBe('Lunch')
  })

  it('is not a rule until somebody chooses where it posts', () => {
    // Nothing the model named exists here, so the draft cannot be saved as is.
    const drafted = suggestedMailRuleForm(suggestion())
    expect(mailRuleDraft(drafted)).toBeNull()
    expect(mailRuleProblem(drafted)).toBe('Choose the account the transaction lands in.')

    const chosen = suggestedMailRuleForm(
      suggestion({ account_id: 'acct-checking', category_id: 'cat-dining' }),
    )
    expect(mailRuleDraft(chosen)).toMatchObject({
      name: 'Lunch receipts',
      amount_label: 'Receipt Total:',
      account_id: 'acct-checking',
      category_id: 'cat-dining',
      date_label: '',
    })
  })

  it('reads a pattern-only amount and a labelled payee as what they are', () => {
    const drafted = suggestedMailRuleForm(
      suggestion({ amount_label: '', amount_pattern: 'Total\\s+\\$([0-9.]+)', payee: '', payee_label: 'Your receipt from' }),
    )
    expect(drafted.amountFrom).toBe('pattern')
    expect(drafted.payeeFrom).toBe('label')
  })
})

describe('a rule that files a bill', () => {
  /** An invented water utility's bill-ready mail, read as a bill. */
  function billForm(over: Partial<MailRuleForm> = {}): MailRuleForm {
    return form({
      name: 'Water bill',
      action: 'bill',
      sender: '@water.example.invalid',
      subjectContains: 'bill is ready',
      amountLabel: 'Amount due:',
      dateLabel: 'Due date:',
      referenceLabel: 'Account number:',
      issuedLabel: 'Statement date:',
      billConnectionId: 'conn-water',
      ...over,
    })
  }

  it('sends the provider and posts nowhere', () => {
    expect(mailRuleDraft(billForm({ notesLabel: 'Item' }))).toMatchObject({
      action: 'bill',
      bill_connection_id: 'conn-water',
      bill_subaccount_id: null,
      issued_label: 'Statement date:',
      reference_label: 'Account number:',
      date_label: 'Due date:',
      // What a bill rule does not use goes out empty rather than stale.
      account_id: null,
      category_id: null,
      payee: '',
      payee_label: '',
      notes_label: '',
      notes_end_label: '',
      pad_income: false,
    })
    expect(mailRuleDraft(billForm({ billSubaccountId: 'sub-house' }))?.bill_subaccount_id).toBe(
      'sub-house',
    )
  })

  it('is not a rule until somebody chooses the provider, and needs no account', () => {
    expect(mailRuleProblem(billForm({ billConnectionId: null }))).toBe(
      'Choose the bill provider the bill is filed on.',
    )
    expect(mailRuleProblem(billForm({ accountId: null, categoryId: null }))).toBeNull()
    expect(mailRuleProblem(billForm({ dateLabel: '' }))).toBe(
      'Say what text comes just before the due date, or read the day it arrived.',
    )
  })

  it('reads back from a stored bill rule, and a transaction rule sends no provider', () => {
    const stored = mailRuleForm(
      rule({
        action: 'bill',
        account_id: null,
        category_id: null,
        issued_label: 'Statement date:',
        bill_connection_id: 'conn-water',
        bill_subaccount_id: 'sub-house',
      }),
    )
    expect(stored.action).toBe('bill')
    expect(stored.billConnectionId).toBe('conn-water')
    expect(stored.billSubaccountId).toBe('sub-house')
    expect(stored.issuedLabel).toBe('Statement date:')
    expect(stored.accountId).toBeNull()

    expect(mailRuleDraft(form({ billConnectionId: 'conn-water' }))).toMatchObject({
      action: 'transaction',
      bill_connection_id: null,
      bill_subaccount_id: null,
      issued_label: '',
    })
  })

  it('says where it files, and the billed account it pins', () => {
    const filing = rule({ action: 'bill', account_id: null, category_id: null })
    expect(describeMailRuleAction({ ...filing, bill_connection_id: 'conn-water' }, names)).toBe(
      'Files a bill on Example Water',
    )
    expect(
      describeMailRuleAction(
        { ...filing, bill_connection_id: 'conn-water', bill_subaccount_id: 'sub-house' },
        names,
      ),
    ).toBe('Files a bill on Example Water · the house')
    expect(describeMailRuleAction({ ...filing, bill_connection_id: 'conn-gone' }, names)).toBe(
      'Files a bill',
    )
  })

  it('opens an assistant draft of a bill as a bill', () => {
    const drafted = suggestedMailRuleForm({
      action: 'bill',
      bill_connection_id: 'conn-water',
      issued_label: 'Statement date:',
      issued_pattern: '',
      minimum_label: '',
      minimum_pattern: '',
      statement_account_id: null,
      name: 'Water bill',
      sender: '@water.example.invalid',
      subject_contains: '',
      body_contains: '',
      amount_label: 'Amount due:',
      amount_pattern: '',
      date_label: 'Due date:',
      date_pattern: '',
      reference_label: 'Account number:',
      reference_pattern: '',
      payee: '',
      payee_label: '',
      notes_label: '',
      notes_end_label: '',
      direction: 'expense',
      account_id: null,
      category_id: null,
      pad_income: false,
      income_account_id: null,
      income_category_id: null,
      income_payee: '',
    })
    expect(drafted.action).toBe('bill')
    expect(drafted.billConnectionId).toBe('conn-water')
    expect(drafted.issuedLabel).toBe('Statement date:')
    expect(mailRuleDraft(drafted)).toMatchObject({ action: 'bill', bill_connection_id: 'conn-water' })
  })
})

describe('a card statement rule', () => {
  const billed = [
    { id: 'sub-card', connection_id: 'conn-card', account_id: null },
    { id: 'sub-a', connection_id: 'conn-many', account_id: 'acct-a' },
    { id: 'sub-b', connection_id: 'conn-many', account_id: null },
  ]
  const bill = (over: Partial<MailRuleForm> = {}): MailRuleForm =>
    form({
      action: 'bill',
      billConnectionId: 'conn-card',
      amountLabel: 'Statement Balance:',
      dateLabel: 'Payment Due Date:',
      minimumLabel: 'Minimum Payment Due:',
      ...over,
    })

  it('sends the minimum payment on a bill rule and never on a transaction rule', () => {
    expect(mailRuleDraft(bill())).toMatchObject({
      minimum_label: 'Minimum Payment Due:',
      minimum_pattern: '',
    })
    expect(mailRuleDraft(form({ minimumLabel: 'Minimum Payment Due:' }))).toMatchObject({
      minimum_label: '',
      minimum_pattern: '',
    })
    expect(mailRuleProblem(bill({ minimumPattern: '([0-9' }))).toBe(
      'That minimum payment pattern is not a valid expression.',
    )
  })

  it('links the one billed account the rule files on, and only that one', () => {
    expect(statementTarget(bill(), billed)?.id).toBe('sub-card')
    expect(statementTarget(bill({ billConnectionId: 'conn-many' }), billed)).toBeNull()
    expect(
      statementTarget(bill({ billConnectionId: 'conn-many', billSubaccountId: 'sub-b' }), billed)?.id,
    ).toBe('sub-b')
    expect(statementTarget(form(), billed)).toBeNull()
  })

  it('sends the link only when it changes', () => {
    const target = billed[1]
    expect(statementLinkChange({ statementAccountId: undefined }, target)).toBeUndefined()
    expect(statementLinkChange({ statementAccountId: 'acct-a' }, target)).toBeUndefined()
    expect(statementLinkChange({ statementAccountId: 'acct-b' }, target)).toBe('acct-b')
    expect(statementLinkChange({ statementAccountId: null }, target)).toBeNull()
    expect(statementLinkChange({ statementAccountId: 'acct-b' }, null)).toBeUndefined()
  })

  it('opens the assistant’s card draft with the minimum and the card it names', () => {
    const draft: MailRuleSuggestion['rule'] = {
      action: 'bill',
      bill_connection_id: 'conn-card',
      issued_label: '',
      issued_pattern: '',
      minimum_label: 'Minimum Payment Due:',
      minimum_pattern: '',
      statement_account_id: 'acct-card',
      name: 'Northwind statement',
      sender: '@alerts.northwind-card.example',
      subject_contains: 'statement is ready',
      body_contains: '',
      amount_label: 'Statement Balance:',
      amount_pattern: '',
      date_label: 'Payment Due Date:',
      date_pattern: '',
      reference_label: '',
      reference_pattern: '',
      payee: '',
      payee_label: '',
      notes_label: '',
      notes_end_label: '',
      direction: 'expense',
      account_id: null,
      category_id: null,
      pad_income: false,
      income_account_id: null,
      income_category_id: null,
      income_payee: '',
    }
    const drafted = suggestedMailRuleForm(draft)
    expect(drafted.minimumLabel).toBe('Minimum Payment Due:')
    expect(drafted.statementAccountId).toBe('acct-card')
    expect(
      suggestedMailRuleForm({ ...draft, statement_account_id: null }).statementAccountId,
      'nothing named leaves the link alone',
    ).toBeUndefined()
  })
})

describe('billMailRuleForm', () => {
  it('starts a bill rule named for and filed on the provider just added', () => {
    const seeded = billMailRuleForm({ id: 'conn-water' }, 'Example Water')
    expect(seeded.action).toBe('bill')
    expect(seeded.name).toBe('Example Water bills')
    expect(seeded.billConnectionId).toBe('conn-water')
  })

  it('leaves only the reading to fill in before it can be saved', () => {
    const seeded = billMailRuleForm({ id: 'conn-water' }, 'Example Water')
    expect(mailRuleProblem(seeded)).toBe('Say what text comes just before the amount.')
    const draft = mailRuleDraft({ ...seeded, amountLabel: 'Amount due:' })
    expect(draft?.bill_connection_id).toBe('conn-water')
  })
})
