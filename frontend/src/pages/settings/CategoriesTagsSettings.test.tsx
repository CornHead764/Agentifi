import { describe, expect, it } from 'vitest'

import { CATEGORIES_KEY, TAGS_KEY } from '@/lib/transactions/cache'
import type { Category, Tag } from '@/lib/transactions/types'
import { category } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'

import { CategoriesTagsSettings } from './CategoriesTagsSettings'

const CATEGORIES: Category[] = [
  category('auto', 'Auto & Transport'),
  category('plate', 'Plate Renewal', 'auto', { txf_id: 'N269' }),
  category('salary', 'Paycheck', null, { kind: 'income' }),
  category('transfer', 'Transfer', null, {
    kind: 'transfer',
    known_category_id: '7125000000',
    protected_reason: 'matched transfers are filed under it',
    excluded_from_reports: true,
  }),
  category('opening', 'Opening Balance', null, {
    kind: 'transfer',
    is_editable: false,
    protected_reason: 'it is maintained by the app',
  }),
]

const TAGS: Tag[] = [{ id: 'reimb', name: 'Reimbursable', color: null }]

function render(
  seeded: boolean,
  categories: Category[] = CATEGORIES,
  path = '/settings/categories-tags',
) {
  return renderScreen(<CategoriesTagsSettings />, {
    seed: seeded
      ? [
          [CATEGORIES_KEY, categories],
          [TAGS_KEY, TAGS],
        ]
      : [],
    route: path,
  })
}

describe('the categories and tags settings page', () => {
  it('renders both halves with no server behind it', () => {
    const markup = render(false)
    expect(markup).toContain('Tags')
    expect(markup).toContain('Categories')
  })

  it('marks the one category a link names, nested or not', () => {
    const markup = render(true, CATEGORIES, '/settings/categories-tags?category=plate')
    const marked = [...markup.matchAll(/<tr[^>]*data-linked="true"[^>]*>(.*?)<\/tr>/g)]
    expect(marked).toHaveLength(1)
    expect(marked[0][1]).toContain('Plate Renewal')
    expect(render(true)).not.toContain('data-linked')
  })

  it('opens no dialog until something is chosen', () => {
    // Every dialog here is a write or a delete confirmation. One rendered on
    // arrival would be a modal nobody asked for.
    expect(render(false)).not.toContain('Create category')
    expect(render(false)).not.toContain('Delete category')
  })

  it('offers the standard set to a space with no categories', () => {
    const markup = render(true, [])
    expect(markup).toContain('No categories yet')
    expect(markup).toContain('Start with the standard set')
  })

  it('links straight to the import card for a Simplifi export', () => {
    const markup = render(true, [])
    expect(markup).toContain('href="/settings/accounts#import"')
    expect(markup).not.toContain('Settings → Accounts → Import a statement says how')
  })

  it('offers the standard set only while the tree is empty', () => {
    expect(render(true)).not.toContain('Start with the standard set')
  })

  it('shows the tree with its type and tax columns', () => {
    const markup = render(true)
    expect(markup).toContain('Tax form &amp; line item')
    expect(markup).toContain('Plate Renewal')
    expect(markup).toContain('N269')
    expect(markup).toContain('Income')
  })

  it('indents a subcategory rather than listing it beside its group', () => {
    expect(render(true)).toContain('cat-name cat-name--1')
  })

  // The API refuses the whole PATCH of a system category, so the row must not
  // offer an action that is going to be turned down.
  it('gives a system category a badge and no actions menu', () => {
    const markup = render(true)
    expect(markup).toContain('System')
    expect(markup).not.toContain('Actions for Opening Balance')
    expect(markup).toContain('Actions for Auto &amp; Transport')
  })

  it('keeps the actions menu on a category that can be renamed but not deleted', () => {
    expect(render(true)).toContain('Actions for Transfer')
  })

  it('shows the two exclusions as separate flags', () => {
    expect(render(true)).toContain('Not in reports')
  })

  it('renders a tag as a chip', () => {
    expect(render(true)).toContain('Reimbursable')
  })

    // The tax column is dropped on a phone rather than scrolled to.
  it('marks the tax column as the one a phone drops', () => {
    const markup = render(true)
    expect(markup).toContain('<th class="hide-narrow">Tax form &amp; line item</th>')
    expect(markup).toContain('<td class="hide-narrow">N269</td>')
  })
})

/**
 * The clean-up action. The dialog itself is a portal, which a static render
 * does not draw; its lists are tested in categories/CleanupUnusedDialog.test.tsx.
 */
describe('cleaning up unused categories and tags', () => {
  it('offers the action beside the category controls', () => {
    expect(render(true)).toContain('Clean up unused')
  })

  it('asks nothing of the server until the action is chosen', () => {
    // The unused list is a query per kind of reference over the whole
    // history; the page must not start one on every visit.
    const markup = render(true)
    expect(markup).not.toContain('Nothing to clean up')
    expect(markup).not.toContain('Checked against')
  })
})
