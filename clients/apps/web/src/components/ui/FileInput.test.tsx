import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { Field } from './Field'
import { FileInput } from './FileInput'

describe('the file input', () => {
  it('says what will happen and what has not been chosen yet', () => {
    const markup = renderToStaticMarkup(<FileInput />)
    expect(markup).toContain('Choose file')
    expect(markup).toContain('No file chosen')
    expect(markup).toContain('file-input__name--empty')
  })

  it('shows the chosen file in place of the placeholder', () => {
    const markup = renderToStaticMarkup(<FileInput fileName="statement.qfx" />)
    expect(markup).toContain('statement.qfx')
    expect(markup).not.toContain('No file chosen')
  })

  it('keeps the field label pointed at the real control', () => {
    const markup = renderToStaticMarkup(
      <Field label="File">
        <FileInput />
      </Field>,
    )
    const forId = /<label class="field__label" for="([^"]+)"/.exec(markup)?.[1]
    expect(forId).toBeTruthy()
    expect(markup).toContain(`type="file" class="visually-hidden" id="${forId}"`)
  })
})
