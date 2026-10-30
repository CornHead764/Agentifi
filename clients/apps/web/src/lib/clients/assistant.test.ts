import { afterEach, describe, expect, it, vi } from 'vitest'

import { startMailConversation } from './assistant'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('a conversation about an email', () => {
  it('names the logged message and nothing of its text', async () => {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      headers: { get: () => null },
      text: () =>
        Promise.resolve(
          JSON.stringify({
            id: 'c1',
            title: '',
            created_at: '2026-09-20T08:00:00Z',
            updated_at: '2026-09-20T08:00:00Z',
            messages: [],
            actions: [],
            mail: null,
          }),
        ),
    })
    vi.stubGlobal('fetch', fetcher)

    await startMailConversation('log-receipt')

    const [url, init] = fetcher.mock.calls[0] as [string, { method: string; body: string }]
    expect(url).toBe('/api/assistant/conversations')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body)).toEqual({ mail_id: 'log-receipt' })
  })
})
