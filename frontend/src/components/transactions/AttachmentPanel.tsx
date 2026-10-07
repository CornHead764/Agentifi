import { useQuery } from '@tanstack/react-query'
import { clsx } from 'clsx'
import { Camera, Download, FileText, Paperclip, Receipt, Trash2 } from 'lucide-react'
import { useState, type ChangeEvent } from 'react'

import { useDownloadDocument, useOpenDocument } from '@/components/documentActions'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Callout,
  ConfirmDialog,
  EmptyState,
  FileInput,
  IconButton,
  RowActions,
  Tooltip,
  useConfirm,
  useToast,
} from '@/components/ui'
import {
  ACCEPTED_ATTACHMENTS,
  CAMERA_ATTACHMENTS,
  MAX_ATTACHMENT_BYTES,
  deleteAttachment,
  formatSize,
} from '@/lib/clients/attachments'
import { useBillsSettledBy } from '@/lib/clients/bills'
import {
  describeOrigin,
  isPreviewableDocument,
  isRemovableFromRow,
  listDocumentsBehind,
  receiptBadge,
  uploadDocument,
  type Document as StoredDocument,
} from '@/lib/clients/documents'
import { formatDate } from '@/lib/format'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { TRANSACTIONS_KEY } from '@/lib/transactions/cache'
import type { ReceiptStatus, Uuid } from '@/lib/transactions/types'
import { useBlobUrl } from '@/lib/useBlobUrl'
import { useMediaQuery } from '@/lib/useMediaQuery'

/**
 * The files behind one transaction: its own attachments plus the statement of
 * the bill it settled and the invoice of the order it matched. Only the row's
 * own files offer Remove. A row whose account requires receipts and has none
 * says so above the pickers.
 *
 * On a touch screen a second picker opens the camera. It is another source
 * for the same upload, not a second path: both hand their file to `choose`.
 *
 * Every file is fetched with the bearer token, never through an `<img src>`:
 * the endpoint checks household membership. The thumbnails' blob URLs are
 * revoked on unmount. A file opens in a new tab, in the browser's own viewer;
 * Download saves it.
 */
export function AttachmentPanel({
  transactionId,
  receiptStatus = null,
}: {
  transactionId: Uuid
  receiptStatus?: ReceiptStatus | null
}) {
  const { show } = useToast()
  const touch = useMediaQuery('(pointer: coarse)')

  const documents = useQuery({
    queryKey: ATTACHMENTS_KEY(transactionId),
    queryFn: ({ signal }) => listDocumentsBehind(transactionId, signal),
  })

  // The register draws a paperclip from `attachment_count`, so both the list
  // here and the row behind the dialog have to be refreshed after a write.
  const invalidates = [ATTACHMENTS_KEY(transactionId), TRANSACTIONS_KEY]

  const add = useInvalidatingMutation(
    (file: File) => uploadDocument({ kind: 'transaction', target_id: transactionId }, file),
    invalidates,
    { failure: 'That file could not be uploaded' },
  )

  const remove = useConfirm(
    useInvalidatingMutation(
      // Scoped to this row: the same file may be attached to another one, and
      // that row's copy is not this panel's to remove.
      (id: Uuid) => deleteAttachment(id, transactionId),
      invalidates,
      { failure: 'That attachment could not be removed' },
    ),
    { variables: (doc: StoredDocument) => doc.id },
  )

  const choose = (files: FileList | null) => {
    const file = files?.[0]
    if (!file) return
    // Refused here as well as on the server, so a ten-megabyte photo does not
    // have to cross the wire to be told no.
    if (file.size > MAX_ATTACHMENT_BYTES) {
      show({
        tone: 'error',
        title: 'That file is too large',
        description: `${file.name} is ${formatSize(file.size)}. An attachment may be at most ${formatSize(MAX_ATTACHMENT_BYTES)}.`,
      })
      return
    }
    add.mutate(file)
  }

  const picked = (event: ChangeEvent<HTMLInputElement>) => {
    choose(event.target.files)
    // Cleared so choosing the same file again still fires a change event.
    event.target.value = ''
  }
  // Read off the list as well as the row: a file added here clears the
  // notice before the register has refetched the row.
  const owesReceipt = receiptStatus === 'missing' && documents.data?.length === 0

  return (
    <div className="txn-attachments">
      <p className="filter-panel__section-title">Attachments</p>
      {owesReceipt ? (
        <Callout tone="warning" icon={<Receipt size={14} />} className="txn-attachments__owed">
          <p>This account needs a receipt for every purchase, and this one has none yet.</p>
        </Callout>
      ) : null}
      <BillsWithoutStatement transactionId={transactionId} />

      <QueryBoundary
        query={documents}
        rows={2}
        empty={(rows) =>
          rows.length === 0 ? (
            <EmptyState compact title="No receipts or statements on this transaction." />
          ) : undefined
        }
      >
        {(rows) => (
          <ul className="txn-attachments__list">
            {rows.map((doc) => (
              <AttachmentRow
                key={doc.id}
                doc={doc}
                busy={remove.dialog.pending}
                onRemove={() => remove.ask(doc)}
              />
            ))}
          </ul>
        )}
      </QueryBoundary>

      {/* The same styled picker the import form uses. A bare file input is
          drawn by the browser and reads as a different application. */}
      <div className="row row--wrap">
        <FileInput
          action={add.isPending ? 'Uploading…' : 'Add a receipt'}
          aria-label="Add a receipt"
          placeholder={null}
          disabled={add.isPending}
          accept={ACCEPTED_ATTACHMENTS}
          onChange={picked}
        />
        {touch ? (
          <FileInput
            action="Take photo"
            aria-label="Take a photo of the receipt"
            icon={<Camera size={13} aria-hidden="true" />}
            placeholder={null}
            disabled={add.isPending}
            accept={CAMERA_ATTACHMENTS}
            capture="environment"
            onChange={picked}
          />
        ) : null}
      </div>
      <p className="hint hint--faint">JPEG, PNG, GIF, WebP, HEIC or PDF, up to 25 MB.</p>

      {/* The same confirmation every other delete asks for. The file goes with
          the last row that holds it, and nothing here can put it back. */}
      <ConfirmDialog
        {...remove.dialog}
        title={remove.target ? `Remove ${remove.target.filename}?` : 'Remove this attachment?'}
        description="Deleted once nothing else holds it. The transaction is untouched."
        confirmLabel="Remove attachment"
      />
    </div>
  )
}

export const ATTACHMENTS_KEY = (transactionId: Uuid) => ['attachments', transactionId] as const

/**
 * The bill this row paid when the provider gave no statement for it, so a
 * payment matched to a bill says so even with no file to show.
 */
function BillsWithoutStatement({ transactionId }: { transactionId: Uuid }) {
  const settled = useBillsSettledBy(transactionId)
  const bare = (settled.data ?? []).filter((bill) => bill.document_id === null)
  if (bare.length === 0) return null
  return (
    <ul className="txn-attachments__bills">
      {bare.map((bill) => (
        <li key={bill.bill_id} className="hint">
          Pays the {bill.provider} bill due {formatDate(bill.due_on)},{' '}
          <Money value={bill.amount_due} tone="neutral" />. No statement document came with it.
        </li>
      ))}
    </ul>
  )
}

function AttachmentRow({
  doc,
  busy,
  onRemove,
}: {
  doc: StoredDocument
  busy: boolean
  onRemove: () => void
}) {
  const previewable = isPreviewableDocument(doc)
  const { url: blobUrl } = useBlobUrl(previewable ? doc.url : null)
  // A HEIC photo is an image most browsers cannot draw; it falls back to the
  // file glyph rather than a broken picture.
  const [undrawable, setUndrawable] = useState(false)
  const thumbnail = undrawable ? null : blobUrl
  const { open, pending: opening } = useOpenDocument('That file could not be opened')
  const { download, pending: downloading } = useDownloadDocument('That file could not be downloaded')
  const badge = receiptBadge(doc)

  return (
    <li className="txn-attachments__row">
      <button
        type="button"
        className={clsx('txn-attachments__thumb', !thumbnail && 'txn-attachments__thumb--file')}
        aria-label={`Open ${doc.filename}`}
        disabled={opening}
        onClick={() => open(doc.id)}
      >
        {thumbnail ? (
          <img src={thumbnail} alt="" onError={() => setUndrawable(true)} />
        ) : (
          <FileText size={18} aria-hidden="true" />
        )}
      </button>

      <span className="stack stack--1 txn-attachments__meta">
        <button
          type="button"
          className="txn-attachments__open"
          title={doc.filename}
          disabled={opening}
          onClick={() => open(doc.id)}
        >
          {opening ? 'Opening…' : doc.filename}
        </button>
        <span className="hint hint--faint txn-attachments__origin">
          {badge && <Badge tone="accent">{badge}</Badge>}
          <span>
            {describeOrigin(doc)} · {formatSize(doc.size_bytes)}
          </span>
        </span>
      </span>

      <RowActions>
        <Tooltip label="Download">
          <IconButton
            label={`Download ${doc.filename}`}
            variant="ghost"
            size="sm"
            disabled={downloading}
            onClick={() => download(doc.id, doc.filename)}
          >
            <Download size={14} />
          </IconButton>
        </Tooltip>
        {/* A receipt goes with its bill or its match, not from here. */}
        {isRemovableFromRow(doc) ? (
          <Tooltip label="Remove this attachment">
            <IconButton
              label={`Remove ${doc.filename}`}
              variant="ghost"
              size="sm"
              disabled={busy}
              onClick={onRemove}
            >
              <Trash2 size={14} />
            </IconButton>
          </Tooltip>
        ) : null}
      </RowActions>
    </li>
  )
}

/**
 * A row whose account requires a receipt and which has none. In a register
 * cell it opens the row, whose panel takes one; on a phone's line, which is
 * already one button onto the row, it is only a glyph.
 */
export function MissingReceiptMark({ onOpen }: { onOpen?: () => void }) {
  const label = 'Needs a receipt'
  if (!onOpen) {
    return (
      <span className="txn-mark txn-mark--static txn-mark--owed" title={label}>
        <Receipt size={13} aria-label={label} />
      </span>
    )
  }
  return (
    <Tooltip label="Needs a receipt: add one">
      <IconButton
        size="sm"
        variant="ghost"
        className="txn-mark txn-mark--owed"
        label="Needs a receipt: add one"
        onClick={onOpen}
      >
        <Receipt size={13} />
      </IconButton>
    </Tooltip>
  )
}

/** The register's paperclip. Nothing is drawn for a row with no files. */
export function AttachmentIndicator({ count }: { count: number }) {
  if (count < 1) return null
  return (
    <Tooltip label={count === 1 ? '1 attachment' : `${count} attachments`}>
      <span className="txn-mark txn-mark--static" data-on="true">
        <Paperclip size={13} aria-label={count === 1 ? '1 attachment' : `${count} attachments`} />
        {count > 1 ? (
          <span className="txn-mark__count" aria-hidden="true">
            {count > 9 ? '9+' : count}
          </span>
        ) : null}
      </span>
    </Tooltip>
  )
}
