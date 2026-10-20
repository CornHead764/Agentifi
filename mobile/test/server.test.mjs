import assert from 'node:assert/strict'
import { test } from 'node:test'
import { checkServer, normalizeServer, savedServer, saveServer, SERVER_KEY } from '../src/server.mjs'

function storage(entries = []) {
  const map = new Map(entries)
  return {
    get length() { return map.size },
    key: (i) => [...map.keys()][i] ?? null,
    getItem: (key) => map.get(key) ?? null,
    setItem: (key, value) => map.set(key, value),
    removeItem: (key) => map.delete(key),
  }
}

test('accepts LAN HTTP, HTTPS, IPv6 and an optional /api suffix', () => {
  for (const [input, expected] of [
    [' http://192.0.2.1:8100/ ', 'http://192.0.2.1:8100'],
    ['https://example.test/api/', 'https://example.test'],
    ['http://[2001:db8::1]:8100/api', 'http://[2001:db8::1]:8100'],
  ]) assert.equal(normalizeServer(input), expected)
})

test('rejects credentials, other schemes, queries, fragments and paths', () => {
  for (const value of ['javascript:alert(1)', 'file:///etc/passwd', 'example.test', 'https://u:p@example.test', 'https://example.test/?token=a', 'https://example.test/#a', 'https://example.test/other']) {
    assert.throws(() => normalizeServer(value))
  }
})

test('changing servers clears token, all user space choices and preferences', () => {
  const store = storage([[SERVER_KEY, 'http://192.0.2.1'], ['agentifi.accessToken', 'invented-token'], ['agentifi.activeSpace.user-a', 'space-a'], ['agentifi.theme', 'light'], ['unrelated', 'keep']])
  saveServer(store, 'https://example.test')
  assert.equal(store.getItem('agentifi.accessToken'), null)
  assert.equal(store.getItem('agentifi.activeSpace.user-a'), null)
  assert.equal(store.getItem('agentifi.theme'), null)
  assert.equal(store.getItem('unrelated'), 'keep')
  assert.equal(savedServer(store), 'https://example.test')
})

test('re-saving the same server preserves the session', () => {
  const store = storage([[SERVER_KEY, 'http://192.0.2.1'], ['agentifi.accessToken', 'invented-token']])
  saveServer(store, 'http://192.0.2.1/api/')
  assert.equal(store.getItem('agentifi.accessToken'), 'invented-token')
})

test('missing and corrupt stored addresses return to setup', () => {
  assert.equal(savedServer(storage()), null)
  assert.equal(savedServer(storage([[SERVER_KEY, 'garbage']])), null)
})

test('connection check sends no bearer token or selected space', async () => {
  await checkServer('http://192.0.2.1', async (url, options) => {
    assert.equal(url, 'http://192.0.2.1/api/auth/first-account')
    assert.deepEqual(options.headers, { Accept: 'application/json' })
    assert.equal(options.credentials, 'omit')
    return Response.json({ open: false })
  })
})

test('a web page, wrong API and network error cannot replace the saved server', async () => {
  for (const response of [new Response('<html>'), Response.json({}), Response.json({ open: false }, { status: 500 })]) {
    await assert.rejects(checkServer('https://example.test', async () => response))
  }
  await assert.rejects(checkServer('https://example.test', async () => { throw new Error('offline') }))
})
