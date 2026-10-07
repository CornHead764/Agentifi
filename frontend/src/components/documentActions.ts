/**
 * The one way a screen opens or saves a stored document.
 *
 * The content endpoint wants the bearer token and this app keeps no session
 * cookie, so a plain `href` is a 401 in a fresh tab. The file is fetched
 * through `api.blob`, which carries the token and the space, and the tab is
 * pointed at an object URL. The blob takes the server's `Content-Type`, so a
 * PDF or an image opens in the browser's own viewer rather than downloading.
 *
 * The tab is claimed on the click: a `window.open` after an await has no user
 * gesture behind it and the browser refuses it. A failed fetch closes that tab
 * and raises the failure toast.
 */

import { useState } from 'react'

import { useFailureToast } from '@/components/ui'
import { fetchDocumentContent } from '@/lib/clients/documents'
import { saveBlob } from '@/lib/saveFile'
import type { Uuid } from '@/lib/transactions/types'

/**
 * How long the object URL stays alive for the new tab to load from. Revoking
 * it at once races the tab's fetch and renders an empty window; never revoking
 * it pins every opened file in memory.
 */
const OBJECT_URL_TTL_MS = 60_000

/**
 * Claim a tab at once and point it at the blob when `load` answers. The tab is
 * opened before anything is awaited; a failed load closes it and rejects.
 */
export function openInNewTab(load: () => Promise<Blob>): Promise<void> {
  const tab = window.open('', '_blank')
  // The opener is nothing the document needs, and a tab that can reach back
  // into this one is a tab that can navigate it.
  if (tab) tab.opener = null

  return load().then(
    (blob) => {
      const url = URL.createObjectURL(blob)
      if (tab) tab.location.replace(url)
      else window.open(url, '_blank', 'noopener')
      window.setTimeout(() => URL.revokeObjectURL(url), OBJECT_URL_TTL_MS)
    },
    (error: unknown) => {
      tab?.close()
      throw error
    },
  )
}

/**
 * Open a stored document in a new tab, with the failure as a toast. `pending`
 * keeps a second click from claiming a second tab.
 */
export function useOpenDocument(failureTitle: string): {
  open: (id: Uuid) => void
  pending: boolean
} {
  const failed = useFailureToast(failureTitle)
  const [pending, setPending] = useState(false)

  const open = (id: Uuid) => {
    if (pending) return
    setPending(true)
    openInNewTab(() => fetchDocumentContent(id))
      .catch(failed)
      .finally(() => setPending(false))
  }

  return { open, pending }
}

/** Save a stored document as a file under its own name, with the failure as a toast. */
export function useDownloadDocument(failureTitle: string): {
  download: (id: Uuid, filename: string) => void
  pending: boolean
} {
  const failed = useFailureToast(failureTitle)
  const [pending, setPending] = useState(false)

  const download = (id: Uuid, filename: string) => {
    if (pending) return
    setPending(true)
    fetchDocumentContent(id)
      .then((blob) => saveBlob(blob, filename))
      .catch(failed)
      .finally(() => setPending(false))
  }

  return { download, pending }
}
