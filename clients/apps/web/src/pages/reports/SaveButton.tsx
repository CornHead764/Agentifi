import { BookmarkPlus, Pencil } from 'lucide-react'
import { useState } from 'react'

import { Button, NameDialog } from '@/components/ui'
import {
  createSavedReport,
  updateSavedReport,
  type SavedReport,
  reportKeys,
} from '@/lib/clients/reports'
import { describeApiError } from '@/lib/errors'
import { useInvalidatingMutation } from '@/lib/queryClient'
import type { ReportTab } from '@/lib/reports/tabs'
import { toFilterItems } from '@/lib/transactions/filter'

/**
 * Save. A first save names and creates the report; later ones update the same
 * row. The dot clears only once the server answers, not optimistically.
 */
export function SaveButton({
  tab,
  items,
  queryText,
  onSaved,
}: {
  tab: ReportTab
  items: ReturnType<typeof toFilterItems>
  queryText: string
  onSaved: (row: SavedReport) => void
}) {
  const [naming, setNaming] = useState(false)
  const save = (title: string) =>
    tab.savedId === null
      ? createSavedReport({ name: title, config: tab.state.config, items, queryText })
      : updateSavedReport(tab.savedId, { name: title, config: tab.state.config, items })
  // Two mutations over one save: the direct one reports inline, the dialog's
  // as a toast over the dialog.
  const saving = useInvalidatingMutation(save, [reportKeys.saved], {
    failure: false,
    onSuccess: onSaved,
  })
  const savingNamed = useInvalidatingMutation(save, [reportKeys.saved], {
    failure: 'Not saved',
    onSuccess: onSaved,
  })
  const name = (open: boolean) => {
    // The dialog says its own failure, so the inline one is only the direct save's.
    saving.reset()
    setNaming(open)
  }

  return (
    <>
      <Button
        variant="primary"
        size="sm"
        disabled={saving.isPending}
        onClick={() => {
          if (tab.savedId === null) {
            name(true)
            return
          }
          saving.mutate(tab.title)
        }}
      >
        <BookmarkPlus size={13} /> {saving.isPending ? 'Saving…' : 'Save'}
      </Button>
      {/* A saved tab's Save writes the name it already has, so without this the
          name a report was first given is the name it keeps forever. */}
      {tab.savedId === null ? null : (
        <Button size="sm" disabled={saving.isPending} onClick={() => name(true)}>
          <Pencil size={13} /> Rename…
        </Button>
      )}
      {/* Said once. The failure is inline beside the button that caused it,
          so a toast saying the same thing is the same news twice. */}
      {saving.error && !naming ? (
        <span className="field__error" role="alert">
          Not saved: {describeApiError(saving.error, 'save')}
        </span>
      ) : null}

      <NameDialog
        open={naming}
        onOpenChange={name}
        title={tab.savedId === null ? 'Save report' : 'Rename report'}
        label="Report name"
        initial={tab.title}
        submitLabel={tab.savedId === null ? 'Save' : 'Rename'}
        onSubmit={(title) => savingNamed.mutateAsync(title)}
      />
    </>
  )
}
