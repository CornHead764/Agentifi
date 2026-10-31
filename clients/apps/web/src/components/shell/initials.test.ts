import { describe, expect, it } from 'vitest'

import { initialsFor } from './initials'

describe('initialsFor', () => {
  it('takes the first and last word of a name', () => {
    expect(initialsFor('Ada Lovelace', 'ada@example.test')).toBe('AL')
    expect(initialsFor('Ada King Lovelace', 'ada@example.test')).toBe('AL')
  })

  it('gives one letter for one word, because two would be somebody else', () => {
    expect(initialsFor('Ada', 'ada@example.test')).toBe('A')
  })

  it('falls back to the address, which every account has', () => {
    expect(initialsFor(null, 'ada@example.test')).toBe('A')
    expect(initialsFor('   ', 'ada@example.test')).toBe('A')
  })

  it('answers null rather than an empty mark when there is nothing to draw', () => {
    expect(initialsFor(null, '')).toBeNull()
  })
})
