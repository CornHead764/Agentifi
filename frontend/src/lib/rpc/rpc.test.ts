/** The Connect stack against a stand-in fetch: what goes out, and how a refusal comes back. */

import { create, toBinary } from '@bufbuild/protobuf'
import { createClient } from '@connectrpc/connect'
import { describe, expect, it, vi } from 'vitest'

import { ProblemSchema } from '@/gen/agentifi/v1/common_pb'
import {
  GetNetWorthRequestSchema,
  GetNetWorthResponseSchema,
  NetWorthService,
} from '@/gen/agentifi/v1/net_worth_pb'
import { TagService, UpdateTagRequestSchema } from '@/gen/agentifi/v1/tag_pb'
import { ApiError } from '@/lib/apiError'
import { moneyFromCents } from '@/lib/money'

import { toApiError } from './errors'
import { createRpcTransport, type Session } from './transport'
import { fromWire, toPatch, toWire } from './wire'

function session(overrides: Partial<Session> = {}): Session {
  return {
    token: () => 'token-1',
    space: () => 'space-1',
    signedOut: vi.fn(),
    spaceGone: vi.fn(),
    ...overrides,
  }
}

function base64(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes))
}

/** A Connect error body carrying a Problem, as the server writes one. */
function refusal(status: number, code: string, message: string, problem: Parameters<typeof create<typeof ProblemSchema>>[1]) {
  const detail = base64(toBinary(ProblemSchema, create(ProblemSchema, problem)))
  return new Response(
    JSON.stringify({ code, message, details: [{ type: 'agentifi.v1.Problem', value: detail }] }),
    { status, headers: { 'Content-Type': 'application/json', 'X-Request-Id': 'req-9' } },
  )
}

describe('the transport', () => {
  it('sends the session and the proto field names to the procedure', async () => {
    const fetch = vi.fn(
      async () =>
        new Response(JSON.stringify({ tag: { id: 't-1', name: 'Work', color: null } }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    )
    const client = createClient(TagService, createRpcTransport({ baseUrl: '/api', session: session(), fetch }))

    await client.updateTag(toPatch(UpdateTagRequestSchema, { tag_id: 't-1' }, { color: null, name: 'Work' }))

    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/agentifi.v1.TagService/UpdateTag')
    const headers = new Headers(init.headers)
    expect(headers.get('Authorization')).toBe('Bearer token-1')
    expect(headers.get('X-Space-Id')).toBe('space-1')
    expect(JSON.parse(new TextDecoder().decode(init.body as Uint8Array))).toEqual({ tag_id: 't-1', name: 'Work', update_mask: 'color,name' })
  })

  it('forgets a refused token, and only when one was sent', async () => {
    const signedIn = session()
    const fetch = vi.fn(async () => refusal(401, 'unauthenticated', 'Could not validate credentials', { status: 401 }))
    const client = createClient(TagService, createRpcTransport({ baseUrl: '/api', session: signedIn, fetch }))
    await expect(client.listTags({})).rejects.toThrow()
    expect(signedIn.signedOut).toHaveBeenCalledOnce()

    const signedOut = session({ token: () => null })
    const anonymous = createClient(TagService, createRpcTransport({ baseUrl: '/api', session: signedOut, fetch }))
    await expect(anonymous.listTags({})).rejects.toThrow()
    expect(signedOut.signedOut).not.toHaveBeenCalled()
  })

  it('drops a space the server says is gone, and not on any other 404', async () => {
    const gone = session()
    let answer = () => refusal(404, 'not_found', 'Space not found', { status: 404, code: 'space_not_found' })
    const fetch = vi.fn(async () => answer())
    const client = createClient(TagService, createRpcTransport({ baseUrl: '/api', session: gone, fetch }))
    await expect(client.listTags({})).rejects.toThrow()
    expect(gone.spaceGone).toHaveBeenCalledOnce()

    answer = () => refusal(404, 'not_found', 'Tag not found', { status: 404 })
    await expect(client.getTag({ tagId: 'x' })).rejects.toThrow()
    expect(gone.spaceGone).toHaveBeenCalledOnce()
  })
})

describe('a refusal as an ApiError', () => {
  async function refused(response: Response): Promise<ApiError> {
    const client = createClient(TagService, createRpcTransport({
      baseUrl: '/api', session: session(), fetch: async () => response,
    }))
    try {
      await client.createTag({ name: '' })
    } catch (error) {
      const mapped = toApiError(error, '/agentifi.v1.TagService/CreateTag')
      if (mapped instanceof ApiError) return mapped
    }
    throw new Error('the call was not refused')
  }

  it('keeps the REST status, detail and request id', async () => {
    const error = await refused(refusal(400, 'failed_precondition', 'a tag named "x" already exists', { status: 409 }))
    expect(error.status).toBe(409)
    expect(error.detail).toBe('a tag named "x" already exists')
    expect(error.requestId).toBe('req-9')
    expect(error.path).toBe('/agentifi.v1.TagService/CreateTag')
  })

  it('reads a 422 as its field errors', async () => {
    const error = await refused(refusal(400, 'invalid_argument', 'name is required', {
      status: 422,
      fields: [{ loc: ['body', 'name'], msg: 'name is required', type: 'missing' }],
    }))
    expect(error.status).toBe(422)
    expect(error.body).toEqual({ detail: [{ loc: ['body', 'name'], msg: 'name is required', type: 'missing' }] })
    expect(error.detail).toBe('name is required')
  })

  it('carries the code a client routes on', async () => {
    const error = await refused(refusal(403, 'permission_denied', 'Set a new password before continuing', {
      status: 403, code: 'password_change_required',
    }))
    expect(error.code).toBe('password_change_required')
  })
})

describe('the wire conversion', () => {
  it('turns every Money into cents and back into a decimal string', () => {
    const message = create(GetNetWorthResponseSchema, {
      change: { amount: '-12.34' },
      groups: [{ kind: 'cash', start: { amount: '1000.00' }, accounts: [{ change: { amount: '0.05' } }] }],
    })
    const plain = fromWire<{
      change: unknown
      groups: { start: unknown; change_pct: unknown; accounts: { change: unknown }[] }[]
      start: unknown
    }>(GetNetWorthResponseSchema, message)

    expect(plain.change).toBe(moneyFromCents(-1234))
    expect(plain.groups[0].start).toBe(moneyFromCents(100_000))
    expect(plain.groups[0].accounts[0].change).toBe(moneyFromCents(5))
    expect(plain.groups[0].change_pct).toBeNull()
    expect(plain.start).toBeNull()
  })

  it('leaves a null field unset on the way out', () => {
    const request = toWire(GetNetWorthRequestSchema, { from: null, to: '2026-08-31' })
    expect(request.from).toBe('')
    expect(request.to).toBe('2026-08-31')
  })

  it('names in the mask exactly the keys a patch sent', () => {
    const request = toPatch(UpdateTagRequestSchema, { tag_id: 't-1' }, { color: null, name: undefined })
    expect(request.updateMask?.paths).toEqual(['color'])
    expect(request.color).toBeUndefined()
  })

  it('reaches a service whose method is GetNetWorth', () => {
    expect(NetWorthService.method.getNetWorth.name).toBe('GetNetWorth')
  })
})
