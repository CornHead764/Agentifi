/**
 * Every query hands TanStack's abort signal to its request, so a re-keyed
 * report does not leave computations running. The exemption is the ad-hoc
 * filter, whose query function creates a row.
 */

import { describe, expect, it } from 'vitest'

const SOURCES = import.meta.glob('/src/**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

/** A file, and text the exempt query function's first lines contain. */
const EXEMPT: readonly [string, string][] = [['/src/lib/transactions/queries.ts', 'FilterWrite']]

describe('query functions', () => {
  it('take the abort signal', () => {
    const missing: string[] = []
    for (const [path, source] of Object.entries(SOURCES)) {
      if (path.includes('.test.')) continue
      for (const match of source.matchAll(/queryFn:([^\n]*(?:\n(?!\s*\w+:)[^\n]*){0,3})/g)) {
        const text = match[1]
        if (/\bsignal\b/.test(text)) continue
        if (EXEMPT.some(([file, marker]) => file === path && text.includes(marker))) continue
        missing.push(`${path}: queryFn:${text.split('\n')[0]}`)
      }
    }
    expect(missing).toEqual([])
  })
})
