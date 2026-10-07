import { StatementButton } from '@/components/StatementButton'
import { type Occurrence, autopayNote, billerPaidNote } from '@/lib/clients/upcoming'

/**
 * The biller's own word on a reminder: the day the money leaves when earlier
 * than the due date, a past-due slot the provider reports paid, and the
 * statement the figure came off. `inline` continues a row's sub line after a
 * " · "; otherwise it is its own wrapping line under a card's amount.
 */
export function BillNote({ occurrence, inline = false }: { occurrence: Occurrence; inline?: boolean }) {
  const notes = [autopayNote(occurrence), billerPaidNote(occurrence)].filter(
    (note): note is string => note !== null,
  )
  const documentId = occurrence.bill?.document_id ?? null
  if (notes.length === 0 && !documentId) return null

  const statement = documentId ? <StatementButton documentId={documentId} /> : null
  if (inline) {
    return (
      <>
        {notes.map((note) => (
          <span key={note}> · {note}</span>
        ))}
        {statement ? <> {statement}</> : null}
      </>
    )
  }
  return (
    <p className="reminder-card__bill">
      {notes.map((note) => (
        <span key={note}>{note}</span>
      ))}
      {statement}
    </p>
  )
}
