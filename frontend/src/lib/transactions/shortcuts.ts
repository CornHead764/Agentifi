/**
 * The search language and the keys that do something, shared by the
 * register's Shortcuts popover and the header's Help. List only documented
 * tokens.
 */

export interface ShortcutGroup {
  heading: string
  hint: string
  rows: [string, string][]
}

export const SEARCH_SHORTCUTS: readonly ShortcutGroup[] = [
  {
    heading: 'Payee or description',
    hint: 'Filter by payee name or any word in the transaction.',
    rows: [
      ['Any field contains this word', 'groceries'],
      ['Exact phrase', '"Walmart Store"'],
      ['Payee contains this word', 'payee:walmart'],
      ['Any of these payees (OR)', 'payee:walmart,target'],
    ],
  },
  {
    heading: 'Date',
    hint: 'Filter to a specific date or time period.',
    rows: [
      ['This calendar month', 'date=this-month'],
      ['Last 30 days', 'date=-30d'],
      ['On or after a date', 'date>=2024-01-01'],
      ['Between two dates', 'date:2024-01-01..2024-03-31'],
    ],
  },
  {
    heading: 'Amount',
    hint: 'Filter by exact amount, comparison, or range.',
    rows: [
      ['More than $100', 'amount>100'],
      ['Around $50 (±10%)', 'amount=50±10%'],
      ['Between $50 and $200', 'amount:50..200'],
      ['Expenses over $500', 'expense>500'],
    ],
  },
  {
    heading: 'Category',
    hint: 'Filter by category, or find the rows nothing has filed yet.',
    rows: [
      ['Match a specific category', 'category:"Dining & Drinks"'],
      ['Exclude a category', 'category!="Dining & Drinks"'],
      ['No category assigned', 'is:uncategorized'],
      ['The assistant could not place it', 'is:undetermined'],
      ['The assistant suggested a category', 'is:suggested'],
    ],
  },
  {
    heading: 'Status & review',
    hint: 'Filter by transaction status or review state.',
    rows: [
      ['Pending transactions', 'is:pending'],
      ['Not yet reviewed', 'not:reviewed'],
      ['Reviewed', 'is:reviewed'],
      ['Has a file or receipt attached', 'is:attached'],
      ['Has nothing attached', 'not:attached'],
      ['Needs a receipt and has none', 'is:missing-receipt'],
    ],
  },
  {
    heading: 'Tags',
    hint: 'Filter by assigned tags.',
    rows: [
      ['Has a specific tag', 'tags:reimbursable'],
      ['Has any tag', 'is:tags'],
      ['Has no tags', 'not:tags'],
    ],
  },
]

/** Only keys the register or search box already honours; there are no global shortcuts. */
export const KEY_SHORTCUTS: readonly ShortcutGroup[] = [
  {
    heading: 'Editing a row',
    hint: 'A register cell edits in place on a single click.',
    rows: [
      ['Commit the cell', 'Enter'],
      ['Abandon the edit', 'Esc'],
      ['Leave the cell, committing', 'Tab'],
    ],
  },
  {
    heading: 'Reviewing a row',
    hint: 'The dialog the review tick opens, where a suggestion is decided.',
    rows: [
      ['Save your edits to the row', 'Enter'],
      ['Close without deciding', 'Esc'],
    ],
  },
  {
    heading: 'Search',
    hint: 'The box above the register.',
    rows: [['Clear the search', 'Esc']],
  },
]
