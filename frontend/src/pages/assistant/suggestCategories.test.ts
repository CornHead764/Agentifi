import { describe, expect, it } from 'vitest'

import { ApiError } from '@/lib/api'

import { DEFAULT_QUERY } from '@/lib/transactions/api'

import { blockedToast, suggestBlock, suggestCount } from './suggestCategories'

const refused = (code: string, status = 409) =>
  new ApiError(status, '/assistant-automations/fire', { detail: 'Refused', code })

describe('why suggesting categories did nothing', () => {
  it('reads each refusal the server codes', () => {
    expect(suggestBlock(refused('assistant_unavailable'))).toBe('assistant_unavailable')
    expect(suggestBlock(refused('changes_off'))).toBe('changes_off')
    expect(suggestBlock(refused('automation_off'))).toBe('automation_off')
  })

  it('leaves any other failure to the plain failure toast', () => {
    expect(suggestBlock(refused('something_else'))).toBeNull()
    expect(suggestBlock(refused('changes_off', 500))).toBeNull()
    expect(suggestBlock(new Error('offline'))).toBeNull()
  })

  it('offers a step for each, not directions to one', () => {
    expect(blockedToast('assistant_unavailable').step).toBe('Set up the assistant')
    expect(blockedToast('changes_off').step).toBe('Turn on suggestions')
    expect(blockedToast('automation_off').step).toBe('Turn it on')
  })

  it('is a toast that times out like any other', () => {
    for (const block of ['assistant_unavailable', 'changes_off', 'automation_off'] as const) {
      const shown = blockedToast(block)
      expect(shown.tone).toBeUndefined()
      expect(shown.duration).toBeUndefined()
    }
  })

  it('says that turning suggestions on changes nothing by itself', () => {
    expect(blockedToast('changes_off').description).toContain('nothing changes until you do')
  })
})

describe('how many rows a request names', () => {
  it('counts named rows, and takes the register’s count for a query', () => {
    expect(suggestCount({ ids: ['a', 'b', 'c'] })).toBe(3)
    expect(suggestCount({ query: DEFAULT_QUERY, count: 5120 })).toBe(5120)
  })
})
