/**
 * Documents: the shared file store beneath `attachments.ts`. A document knows
 * its source and what it is a document of. Content is fetched as a Blob,
 * never an `<img src>`, because the content endpoint needs the bearer token.
 */

import { api } from '@/lib/api'
import { formatDate } from '@/lib/format'
import type { Uuid } from '@/lib/transactions/types'

/** The server validates the same set. */
export type DocumentLinkKind = 'bill' | 'transaction' | 'merchant_order' | 'receipt'

export type DocumentSource = 'upload' | 'bill_pull' | 'merchant_pull' | 'email' | 'receipt_scan'

/**
 * What a receipt is the paperwork of: a bill (`name` is the provider
 * connection's label) or a merchant order (`name` is the merchant).
 */
export interface ReceiptOf {
  kind: 'bill' | 'merchant_order'
  name: string
  due_on?: string
  order_number?: string
}

export interface Document {
  id: Uuid
  filename: string
  /** Sniffed from the bytes by the server. */
  content_type: string
  size_bytes: number
  /** Relative to the API base. */
  url: string
  source: DocumentSource
  /** Never a credential. */
  source_ref: string
  /**
   * Only on a `transaction_id` listing: `transaction` when a person attached
   * it, `receipt` when it was filed on the row as its bill's statement or its
   * order's invoice.
   */
  via?: 'transaction' | 'receipt'
  /** Set on a receipt, and on an attachment that is also the row's receipt. */
  receipt_of?: ReceiptOf
  uploaded_by_user_id: Uuid | null
  created_at: string
}

/**
 * What an uploaded file is a document of. The server takes uploads for a
 * transaction or a bill only; `role` is `attachment` when omitted, and a
 * bill's new `statement` unlinks the one it had.
 */
export interface DocumentUploadLink {
  kind: Extract<DocumentLinkKind, 'bill' | 'transaction'>
  target_id: Uuid
  role?: 'attachment' | 'statement'
}

export function uploadDocument(link: DocumentUploadLink, file: File): Promise<Document> {
  const fields: Record<string, string> = { kind: link.kind, target_id: link.target_id }
  if (link.role) fields.role = link.role
  return api.upload<Document>('/documents', file, fields)
}

/** What the row holds and what stands behind it, e.g. its bill's statement. */
export function listDocumentsBehind(
  transactionId: Uuid,
  signal?: AbortSignal,
): Promise<Document[]> {
  return api.get<Document[]>(`/documents?transaction_id=${transactionId}`, undefined, signal)
}

/** For a caller holding the id alone (e.g. a bill's `document_id`). */
export function fetchDocumentContent(id: Uuid, signal?: AbortSignal): Promise<Blob> {
  return api.blob(documentContentUrl(id), signal)
}

/** Needs the bearer token: fetch it as a Blob, never an `<img src>`. */
export function documentContentUrl(id: Uuid): string {
  return `/documents/${id}/content`
}

export function isPreviewableDocument(document: Document): boolean {
  return document.content_type.startsWith('image/')
}

/** What a receipt is the paperwork of first: it explains why a file nobody attached is on this row. */
export function describeOrigin(document: Document): string {
  const of = document.receipt_of
  if (of?.kind === 'bill') {
    const from = of.name ? `Statement from ${of.name}` : "The bill's statement"
    return of.due_on ? `${from}, due ${formatDate(of.due_on)}` : from
  }
  if (of?.kind === 'merchant_order') {
    const order = of.name ? `${of.name} order` : 'Order'
    return of.order_number ? `${order} ${of.order_number} invoice` : `${order} invoice`
  }
  switch (document.source) {
    case 'bill_pull':
      return 'Pulled from the provider'
    case 'merchant_pull':
      return 'From the merchant'
    case 'email':
      return 'From an email'
    case 'receipt_scan':
      return 'Scanned receipt'
    default:
      return 'Uploaded'
  }
}

/** A receipt comes and goes with its bill or its match, never by hand. */
export function isRemovableFromRow(document: Document): boolean {
  return document.via !== 'receipt'
}

/** The mark on a receipt: whose paperwork it is. Null for a file a person attached. */
export function receiptBadge(document: Document): string | null {
  if (document.via !== 'receipt') return null
  if (document.receipt_of?.kind === 'bill') return 'From the bill'
  if (document.receipt_of?.kind === 'merchant_order') return 'From the order'
  return 'Receipt'
}
