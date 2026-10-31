/** The Statement control on a reminder, opening the bill's stored statement in a new tab. */

import { clsx } from 'clsx'
import { FileText } from 'lucide-react'

import { useOpenDocument } from '@/components/documentActions'
import type { Uuid } from '@/lib/transactions/types'

/** The Statement control on a reminder: a real button, with no URL to copy out of it. */
export function StatementButton({
  documentId,
  className,
}: {
  documentId: Uuid
  className?: string
}) {
  const { open, pending } = useOpenDocument('That statement could not be opened')

  return (
    <button
      type="button"
      className={clsx('statement-link', className)}
      disabled={pending}
      onClick={() => open(documentId)}
    >
      <FileText size={12} /> {pending ? 'Opening…' : 'Statement'}
    </button>
  )
}
