import { useState } from 'react'

import {
  useCreateGuidance,
  useDeleteGuidance,
  useGuidance,
  useReorderGuidance,
  useSetGuidanceActive,
  useUpdateGuidance,
  type Guidance,
} from '@/lib/clients/guidance'
import { useAccounts, useCategories, useTags } from '@/lib/transactions/queries'

import { GuidanceEditor } from './GuidanceEditor'
import { ListPanel } from './ListPanel'

/**
 * Guidance notes: what you know about a payee that the ledger cannot say,
 * handed to the assistant beside the row's facts. Nothing here matches or
 * rewrites anything, so rows carry a switch and no *Review existing
 * transactions*.
 *
 * The conditions decide when a note is said and need at least one. Notes about
 * the same payee arrive as one paragraph in list order, which is why the rows
 * have move controls.
 */
export function GuidancePanel({
  creating,
  onCreatingChange,
}: {
  /** The new-note editor, opened from the page header. */
  creating: boolean
  onCreatingChange: (open: boolean) => void
}) {
  const guidance = useGuidance()
  const accounts = useAccounts()
  const categories = useCategories()
  const tags = useTags()

  const create = useCreateGuidance()
  const update = useUpdateGuidance()

  const [editing, setEditing] = useState<{ note: Guidance | null } | null>(null)
  const open = editing ?? (creating ? { note: null } : null)

  return (
    <ListPanel
      kind="guidance"
      query={guidance}
      rows={guidance.data ?? []}
      // The sentence is the point of the row: two lines of it, and the whole
      // of it as the tooltip.
      then={(note) => (
        <span className="rule-prose" title={note.instruction}>
          {note.instruction}
        </span>
      )}
      reorder={useReorderGuidance()}
      setActive={useSetGuidanceActive()}
      remove={useDeleteGuidance()}
      onEdit={(note) => setEditing({ note })}
    >
      {open ? (
        <GuidanceEditor
          note={open.note}
          accounts={accounts.data ?? []}
          categories={categories.data ?? []}
          tags={tags.data ?? []}
          pending={create.isPending || update.isPending}
          onSubmit={(body) => {
            const note = open.note
            if (note !== null) return update.mutateAsync({ id: note.id, patch: body })
            // A create always carries conditions — the editor will not submit
            // without them — and says so in the type it hands the server.
            const { conditions = [], ...rest } = body
            return create.mutateAsync({ ...rest, conditions })
          }}
          onClose={() => {
            setEditing(null)
            onCreatingChange(false)
          }}
        />
      ) : null}
    </ListPanel>
  )
}
