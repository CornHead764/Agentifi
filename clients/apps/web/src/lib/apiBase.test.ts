import { afterEach, describe, expect, it, vi } from 'vitest'

// The base is read at module load, so each case re-imports a fresh copy.
describe('API_BASE', () => {
  afterEach(() => {
    vi.unstubAllEnvs()
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
})
