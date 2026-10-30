/**
 * A rule files transactions under a category, so it offers what the register's
 * category picker offers: the tree, less the system categories and the hidden
 * ones.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog and the
// picker's popover are replaced with inline shells; everything inside them is
// the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
  Popover: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  PopoverTrigger: ({ children }: { children?: React.ReactNode }) => <>{children}</>,
  PopoverContent: ({ children }: { children?: React.ReactNode }) => (
    <div data-testid="picker">{children}</div>
  ),
}))

import type { Rule } from '@/lib/clients/rules'
import { category } from '@/test/builders'
import { renderScreen } from '@/test/renderScreen'

import { RuleEditor } from './RuleEditor'

const CATEGORIES = [
  category('food', 'Food & Dining'),
  category('fast', 'Fast Food', 'food'),
  category('opening', 'Opening Balance', null, { is_user_assignable: false }),
  category('hidden', 'Old Hobbies', null, { excluded_from_category_list: true }),
]

function render(rule: Rule | null = null): string {
  return renderScreen(
    <RuleEditor
      rule={rule}
      accounts={[]}
      categories={CATEGORIES}
      tags={[]}
      pending={false}
      onSubmit={() => Promise.resolve()}
      onClose={() => {}}
    />,
  )
}

/** The labels of the category picker's options, in order. */
function pickerOptions(markup: string): string[] {
  const picker = markup.slice(markup.indexOf('data-testid="picker"'))
  return [...picker.matchAll(/class="picker__option[^"]*"[^>]*>([^<]*)/g)].map((match) => match[1])
}

describe('the rule editor’s category action', () => {
  it('offers the picker’s tree: leave alone, then assignable, visible categories', () => {
    expect(pickerOptions(render())).toEqual(['Leave alone', 'Food &amp; Dining', 'Fast Food'])
  })

  it('draws a subcategory under its parent', () => {
    expect(render()).toMatch(/class="picker__option picker__option--child"[^>]*>Fast Food/)
  })

  it('names the category a rule already sets on its trigger', () => {
    const rule = {
      id: 'r-1',
      name: 'Burgers',
      owns_filter: false,
      filter: { items: [] },
      actions: {
        set_payee: null,
        set_category_id: 'fast',
        add_tag_ids: [],
        set_notes: null,
        set_excluded_from_reports: null,
        set_excluded_from_spending_plan: null,
        set_is_reviewed: null,
      },
    } as unknown as Rule
    expect(render(rule)).toMatch(/class="select__trigger"[^>]*>Fast Food</)
    expect(render()).toMatch(/class="select__trigger"[^>]*>Leave alone</)
  })
})
