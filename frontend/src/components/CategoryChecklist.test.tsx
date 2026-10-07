import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { CategoryChecklist } from './CategoryChecklist'

const CATEGORIES = [
  { id: 'auto', name: 'Auto & Transport', parent_id: null },
  { id: 'reg', name: 'Registration', parent_id: 'auto' },
  { id: 'plate', name: 'Plate Renewal', parent_id: 'reg' },
]

const rowClasses = (markup: string) =>
  [...markup.matchAll(/<div class="(option-list__row[^"]*)"/g)].map((match) => match[1])

describe('<CategoryChecklist>', () => {
  it('draws the three levels indented, with the extra rows above the tree', () => {
    const markup = renderToStaticMarkup(
      <CategoryChecklist
        categories={CATEGORIES}
        selected={['reg']}
        onChange={() => undefined}
        extraRows={[
          { id: 'none', label: 'Uncategorized', checked: false, onCheckedChange: () => undefined },
          { id: 'undetermined', label: 'Undetermined', checked: true, depth: 1, onCheckedChange: () => undefined },
        ]}
      />,
    )
    expect(rowClasses(markup)).toEqual([
      'option-list__row',
      'option-list__row option-list__row--child',
      'option-list__row',
      'option-list__row option-list__row--child',
      'option-list__row option-list__row--grandchild',
    ])
    expect(markup.indexOf('Uncategorized')).toBeLessThan(markup.indexOf('Auto &amp; Transport'))
  })

  it('says the list is empty only when there is nothing to check at all', () => {
    const empty = renderToStaticMarkup(
      <CategoryChecklist categories={[]} selected={[]} onChange={() => undefined} emptyLabel="Nothing here." />,
    )
    expect(empty).toBe('<div class="empty empty--compact"><p class="empty__title">Nothing here.</p></div>')

    const extras = renderToStaticMarkup(
      <CategoryChecklist
        categories={[]}
        selected={[]}
        onChange={() => undefined}
        extraRows={[{ id: 'none', label: 'Uncategorized', checked: false, onCheckedChange: () => undefined }]}
      />,
    )
    expect(extras).toContain('Uncategorized')
    expect(extras).not.toContain('No matching category')
  })
})
