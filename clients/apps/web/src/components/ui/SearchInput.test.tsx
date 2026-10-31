import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { Field } from './Field'
import { SearchInput } from './SearchInput'

const noop = () => undefined

describe('the search input', () => {
  it('offers a clear button only once there is text to clear', () => {
    const empty = renderToStaticMarkup(<SearchInput value="" onChange={noop} aria-label="Search" />)
    const typed = renderToStaticMarkup(
      <SearchInput value="rent" onChange={noop} aria-label="Search" />,
    )
    expect(empty).not.toContain('Clear the search')
    expect(typed).toContain('aria-label="Clear the search"')
  })

  it('keeps the magnifier out of the accessibility tree', () => {
    const markup = renderToStaticMarkup(<SearchInput value="" onChange={noop} aria-label="Search" />)
    expect(markup).toMatch(/<svg[^>]*aria-hidden="true"/)
  })

  it('takes its label from a surrounding field', () => {
    const markup = renderToStaticMarkup(
      <Field label="Find a payee">
        <SearchInput value="" onChange={noop} />
      </Field>,
    )
    const id = /<label[^>]*for="([^"]+)"/.exec(markup)?.[1]
    expect(id).toBeDefined()
    expect(markup).toContain(`class="search__input" autoComplete="off" id="${id}"`)
  })
})
