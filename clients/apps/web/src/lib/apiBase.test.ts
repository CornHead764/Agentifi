import { afterEach, describe, expect, it, vi } from 'vitest'

// The base is read at module load, so each case re-imports a fresh copy.
describe('API_BASE', () => {
  afterEach(() => {
    vi.unstubAllEnvs()
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  it('defaults to the embedded-serve prefix', async () => {
    vi.resetModules()
    const { API_BASE } = await import('./api')
    expect(API_BASE).toBe('/api')
  })

  it('honors VITE_API_BASE — the OIDC anchor is built from it too', async () => {
    vi.stubEnv('VITE_API_BASE', 'https://api.example.test')
    vi.resetModules()
    const { API_BASE } = await import('./api')
    expect(API_BASE).toBe('https://api.example.test')
  })

  describe('sends the session cookie', () => {
    async function credentialsSent(base: string | undefined): Promise<RequestCredentials | undefined> {
      if (base !== undefined) vi.stubEnv('VITE_API_BASE', base)
      vi.stubGlobal('location', { origin: 'https://money.example.test' })
      const fetcher = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        text: () => Promise.resolve('{}'),
      })
      vi.stubGlobal('fetch', fetcher)
      vi.resetModules()
      const { api } = await import('./api')
      await api.get('/health')
      return (fetcher.mock.calls[0]?.[1] as RequestInit).credentials
    }

    it('to its own origin only, when the API is served with the app', async () => {
      expect(await credentialsSent(undefined)).toBe('same-origin')
      expect(await credentialsSent('https://money.example.test/api')).toBe('same-origin')
    })

    it('across origins, when the API is on another one', async () => {
      expect(await credentialsSent('https://api.example.test/api')).toBe('include')
    })
  })
})
