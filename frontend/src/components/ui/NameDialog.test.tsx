import { isValidElement } from 'react'
import { describe, expect, it } from 'vitest'

import { FormDialog } from './FormDialog'
import { NameDialog } from './NameDialog'

/**
 * Callers pass no key, so the typed name must live under the dialog that
 * starts fresh on each opening rather than beside it; otherwise reopening
 * shows the name typed last time.
 */
describe('NameDialog', () => {
  it('keeps the typed name inside the part that remounts on every opening', () => {
    const element = NameDialog({
      title: 'New space',
      open: true,
      onOpenChange: () => undefined,
      onSubmit: () => Promise.resolve(),
    })
    expect(isValidElement(element)).toBe(true)
    expect(element.type).toBe(FormDialog)
  })
})
