/**
 * Attachments on a transaction: documents with a `transaction` link, added
 * through `uploadDocument`. Content is fetched as a Blob, never an
 * `<img src>`, because the content endpoint needs the bearer token.
 */

import { api } from '@/lib/api'
import type { Uuid } from '@/lib/transactions/types'

/** Matches the server's allowlist. HEIC and HEIF are what an iPhone saves. */
export const ACCEPTED_ATTACHMENTS =
  'image/jpeg,image/png,image/gif,image/webp,image/heic,image/heif,application/pdf'

/** What the camera picker asks for: any photo, which the server sniffs and checks. */
export const CAMERA_ATTACHMENTS = 'image/*'

/** The server's limit, restated so the control can refuse early. */
export const MAX_ATTACHMENT_BYTES = 25 * 1024 * 1024

/**
 * The id is the document's. Without `transactionId` every row holding the file
 * lets go of it; the file is deleted once nothing holds it.
 */
export function deleteAttachment(id: Uuid, transactionId?: Uuid): Promise<void> {
  const scope = transactionId ? `?transaction_id=${transactionId}` : ''
  return api.delete<void>(`/attachments/${id}${scope}`)
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}
