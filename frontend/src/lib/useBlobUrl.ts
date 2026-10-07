import { useEffect, useState } from 'react'

import { api } from '@/lib/api'

export interface BlobUrl {
  /** Null while loading, for a null path, and once the fetch has failed. */
  url: string | null
  failed: boolean
}

const LOADING: BlobUrl = { url: null, failed: false }
const FAILED: BlobUrl = { url: null, failed: true }

/**
 * An object URL for bytes that need the bearer token, so cannot be an
 * `<img src>` of their own; revoked on unmount. Not a react-query cache: a
 * cached URL whose blob was revoked would render broken on the second open.
 */
export function useBlobUrl(path: string | null): BlobUrl {
  const [state, setState] = useState<{ path: string; blob: BlobUrl } | null>(null)

  // Keyed on the path alone: react-query returns a fresh object on every
  // refetch, which would download the bytes again.
  useEffect(() => {
    if (path === null) return
    let revoked = false
    let created: string | null = null
    const abort = new AbortController()

    api
      .blob(path, abort.signal)
      .then((blob) => {
        if (revoked) return
        created = URL.createObjectURL(blob)
        setState({ path, blob: { url: created, failed: false } })
      })
      .catch(() => {
        if (!revoked) setState({ path, blob: FAILED })
      })

    return () => {
      revoked = true
      abort.abort()
      if (created) URL.revokeObjectURL(created)
    }
  }, [path])

  return state !== null && state.path === path ? state.blob : LOADING
}
