/**
 * localStorage for preferences, falling back silently: Safari private browsing
 * throws on `setItem`, a browser with site data blocked throws on reading
 * `localStorage` or on `getItem`, and there is no `window` under the server
 * renderer tests use. Every key is namespaced under `agentifi.`.
 */

const PREFIX = 'agentifi.'

function storage(): Storage | null {
  try {
    return typeof window === 'undefined' ? null : window.localStorage
  } catch {
    return null
  }
}

export function readStored(key: string): string | null {
  try {
    return storage()?.getItem(PREFIX + key) ?? null
  } catch {
    return null
  }
}

export function writeStored(key: string, value: string): void {
  try {
    storage()?.setItem(PREFIX + key, value)
  } catch {
    // A preference that will not persist is not worth interrupting the session.
  }
}

/** Forget a value, as opposed to storing `''`, which is neither absent nor a real id. */
export function clearStored(key: string): void {
  try {
    storage()?.removeItem(PREFIX + key)
  } catch {
    // A preference that will not persist is not worth interrupting the session.
  }
}

/** Constrained to `allowed`, so a hand-edited or stale value falls back rather than being cast. */
export function readStoredChoice<T extends string>(
  key: string,
  allowed: readonly T[],
  fallback: T,
): T {
  const raw = readStored(key)
  return allowed.find((choice) => choice === raw) ?? fallback
}

/** Written as `true`/`false`. `1`/`0` are read too, since storage can hold a flag in that shape. */
export function readStoredFlag(key: string, fallback: boolean): boolean {
  const raw = readStored(key)
  if (raw === 'true' || raw === '1') return true
  if (raw === 'false' || raw === '0') return false
  return fallback
}

export function writeStoredFlag(key: string, value: boolean): void {
  writeStored(key, String(value))
}

/**
 * The stored value parsed as JSON, or null when it is absent or does not
 * parse. Its shape is unchecked: the caller validates what it reads.
 */
export function readStoredJson(key: string): unknown {
  const raw = readStored(key)
  if (raw === null) return null
  try {
    const parsed: unknown = JSON.parse(raw)
    return parsed
  } catch {
    return null
  }
}

export function writeStoredJson(key: string, value: unknown): void {
  writeStored(key, JSON.stringify(value))
}

/** Calls `listener` when another tab changes `key`. */
export function onStoredChange(key: string, listener: () => void): () => void {
  if (typeof window === 'undefined') return () => undefined
  const changed = (event: StorageEvent) => {
    if (event.key === PREFIX + key) listener()
  }
  window.addEventListener('storage', changed)
  return () => window.removeEventListener('storage', changed)
}
