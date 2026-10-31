import { describe, expect, it } from 'vitest'

import { afterSignInOf, takesMailedCode } from './signInFlow'

describe('a code answered from the mailbox', () => {
  it('moves the dialog on while it is still on the step it was watching', () => {
    expect(takesMailedCode({ mailed_code_found: true }, 'sess-1', 'sess-1')).toBe(true)
  })

  it('drops it once the person has answered themself', () => {
    // Submitting clears what is being watched; the mailed answer that lands
    // afterwards would be a second answer to the same question.
    expect(takesMailedCode({ mailed_code_found: true }, null, 'sess-1')).toBe(false)
  })

  it('drops it when nothing arrived, or it was for another sign-in', () => {
    expect(takesMailedCode({ mailed_code_found: false }, 'sess-1', 'sess-1')).toBe(false)
    expect(takesMailedCode({ mailed_code_found: true }, 'sess-2', 'sess-1')).toBe(false)
  })
})

describe('after the sign-in is kept', () => {
  it('is nothing until then', () => {
    expect(afterSignInOf(false, { ok: true, note: 'x' })).toBeNull()
  })

  it('fetches, then finishes or stops with the pull’s own note', () => {
    expect(afterSignInOf(true, null)).toEqual({ phase: 'fetching', note: '' })
    expect(afterSignInOf(true, { ok: true, note: 'Fetched.' })).toEqual({
      phase: 'finished',
      note: 'Fetched.',
    })
    expect(afterSignInOf(true, { ok: false, note: 'Refused.' })).toEqual({
      phase: 'stopped',
      note: 'Refused.',
    })
  })
})
