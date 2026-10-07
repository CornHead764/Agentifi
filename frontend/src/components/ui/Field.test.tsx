/**
 * A field's id, and who may have it. A group labels a set of controls and must
 * not hand them its id: a label whose `for` names a duplicated id activates
 * the first element in the document.
 */

import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { Checkbox } from './Checkbox'
import { Field } from './Field'

function ids(markup: string): string[] {
  return [...markup.matchAll(/\bid="([^"]+)"/g)].map((match) => match[1])
}

describe('a field', () => {
  it('gives every control in a group an id of its own', () => {
    const markup = renderToStaticMarkup(
      <Field label="Accounts" as="group">
        <Checkbox label="Everyday Checking" />
        <Checkbox label="Sample Rewards Card" />
        <Checkbox label="Emergency Savings" />
      </Field>,
    )
    const found = ids(markup)
    expect(found).toHaveLength(3)
    expect(new Set(found).size).toBe(3)
  })

  it('points each label at the box beside it', () => {
    const markup = renderToStaticMarkup(
      <Field label="Accounts" as="group">
        <Checkbox label="Everyday Checking" />
        <Checkbox label="Sample Rewards Card" />
      </Field>,
    )
    const pairs = [...markup.matchAll(/id="([^"]+)"[\s\S]*?for="([^"]+)">([^<]+)</g)]
    expect(pairs).toHaveLength(2)
    for (const [, boxId, forId] of pairs) expect(forId).toBe(boxId)
  })

  it('still lends its id to the one control a plain field labels', () => {
    // The whole point of the context: a label that cannot drift from its input.
    const markup = renderToStaticMarkup(
      <Field label="Name">
        <Checkbox />
      </Field>,
    )
    const [, forId] = markup.match(/for="([^"]+)"/) ?? []
    expect(ids(markup)).toContain(forId)
  })
})
