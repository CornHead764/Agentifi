export const SERVER_KEY = 'agentifi.mobile.server'

export function normalizeServer(value) {
  const url = new URL(value.trim())
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
    throw new Error('Enter an HTTP or HTTPS server address without credentials, a query or a fragment.')
  }
  if (url.pathname !== '/' && url.pathname !== '/api' && url.pathname !== '/api/') {
    throw new Error('Enter the server address, for example http://10.10.20.101:8100, without a path.')
  }
  return url.origin
}

export function savedServer(storage) {
  const value = storage.getItem(SERVER_KEY)
  if (!value) return null
  try {
    return normalizeServer(value)
  } catch {
    return null
  }
}

export function saveServer(storage, server) {
  const normalized = normalizeServer(server)
  if (savedServer(storage) !== normalized) {
    // Session, space choices and user preferences share this namespace.
    const keys = Array.from({ length: storage.length }, (_, i) => storage.key(i))
    for (const key of keys) {
      if (key?.startsWith('agentifi.')) storage.removeItem(key)
    }
  }
  storage.setItem(SERVER_KEY, normalized)
  return normalized
}

export async function checkServer(server, fetcher = fetch) {
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 10_000)
  try {
    const response = await fetcher(`${server}/api/auth/first-account`, {
      headers: { Accept: 'application/json' },
      credentials: 'omit',
      signal: controller.signal,
    })
    if (!response.ok || !response.headers.get('content-type')?.includes('application/json')) {
      throw new Error('This address did not answer as an Agentifi server.')
    }
    const status = await response.json()
    if (typeof status?.open !== 'boolean') {
      throw new Error('This address did not answer as an Agentifi server.')
    }
  } finally {
    clearTimeout(timeout)
  }
}
