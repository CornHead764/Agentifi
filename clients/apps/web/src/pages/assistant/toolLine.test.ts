import { describe, expect, it } from 'vitest'

import { toolLine } from './toolLine'

describe('the assistant’s audit line', () => {
  it('says what was done and what it was done to', () => {
    expect(toolLine('read', 'list_accounts')).toBe('read list accounts')
    expect(toolLine('asked to', 'update_transaction')).toBe('asked to update transaction')
  })

  // The whole point of the verb: a tool that creates a tag must never be
  // introduced as something that was read.
  it('does not describe a change as a look', () => {
    expect(toolLine('asked to', 'create_tag')).toBe('asked to create tag')
  })

  it('does not say the verb twice when the name already leads with it', () => {
    expect(toolLine('read', 'read_endpoint')).toBe('read endpoint')
    expect(toolLine('asked to', 'change_endpoint')).toBe('asked to change endpoint')
  })

  // "readme" is not "read me": only a whole word counts as the verb.
  it('matches the verb as a word, not as a prefix', () => {
    expect(toolLine('read', 'readme_file')).toBe('read readme file')
  })
})
