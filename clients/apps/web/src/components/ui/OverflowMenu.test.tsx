import type { ReactNode } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the menu parts are
// inline shells that keep what the assertions read: order, rules and danger.
vi.mock('./DropdownMenu', () => {
  const Pass = ({ children }: { children?: ReactNode }) => <>{children}</>
  return {
    DropdownMenu: Pass,
    DropdownMenuTrigger: Pass,
    DropdownMenuContent: ({ children, align }: { children?: ReactNode; align?: string }) => (
      <div data-align={align}>{children}</div>
    ),
    DropdownMenuItem: ({ children, danger }: { children?: ReactNode; danger?: boolean }) => (
      <div data-item={danger ? 'danger' : 'safe'}>{children}</div>
    ),
    DropdownMenuCheckboxItem: ({ children }: { children?: ReactNode }) => <div data-item="check">{children}</div>,
    DropdownMenuLabel: ({ children }: { children?: ReactNode }) => <h3>{children}</h3>,
    DropdownMenuSeparator: () => <hr />,
    DropdownMenuSub: Pass,
    DropdownMenuSubTrigger: ({ children }: { children?: ReactNode }) => <div data-item="sub">{children}</div>,
    DropdownMenuSubContent: Pass,
  }
})

import { OverflowMenu } from './OverflowMenu'

/** The menu read top to bottom: item labels, `---` for a rule, `# x` for a heading. */
function outline(markup: string): string[] {
  const content = markup.slice(markup.indexOf('data-align'))
  return [...content.matchAll(/<hr\/>|<h3>([^<]*)<\/h3>|data-item="(\w+)">([^<]*)/g)].map((match) => {
    if (match[0] === '<hr/>') return '---'
    if (match[1] !== undefined) return `# ${match[1]}`
    return match[2] === 'danger' ? `!${match[3]}` : match[3]
  })
}

const noop = () => undefined

describe('<OverflowMenu>', () => {
  it('draws destructive actions last, under their own rule, wherever they were listed', () => {
    const markup = renderToStaticMarkup(
      <OverflowMenu
        label="Actions for Groceries"
        actions={[
          { label: 'Delete', danger: true, onSelect: noop },
          { label: 'Edit', onSelect: noop },
          false,
          { label: 'Duplicate', onSelect: noop },
        ]}
        sections={[
          {
            label: 'Exclude from',
            entries: [
              { label: 'Reports', checked: true, onCheckedChange: noop },
              { label: 'Archive', danger: true, onSelect: noop },
            ],
          },
          { entries: [null] },
        ]}
      />,
    )
    expect(outline(markup)).toEqual([
      'Edit',
      'Duplicate',
      '---',
      '# Exclude from',
      'Reports',
      '---',
      '!Delete',
      '!Archive',
    ])
  })

  it('names the thing it acts on, and aligns to the trigger’s end', () => {
    const markup = renderToStaticMarkup(
      <OverflowMenu label="Actions for Groceries" actions={[{ label: 'Edit', onSelect: noop }]} />,
    )
    expect(markup).toContain('aria-label="Actions for Groceries"')
    expect(markup).toContain('data-align="end"')
  })

  it('draws no rule when there is nothing safe above the danger', () => {
    const markup = renderToStaticMarkup(
      <OverflowMenu label="Actions" actions={[{ label: 'Remove', danger: true, onSelect: noop }]} />,
    )
    expect(outline(markup)).toEqual(['!Remove'])
  })

  it('opens from an invisible anchor instead of a button when asked', () => {
    const markup = renderToStaticMarkup(
      <OverflowMenu label="Actions for Coffee" anchorOnly actions={[{ label: 'Edit', onSelect: noop }]} />,
    )
    expect(markup).toContain('row-menu__anchor')
    expect(markup).not.toContain('Actions for Coffee')
  })
})
