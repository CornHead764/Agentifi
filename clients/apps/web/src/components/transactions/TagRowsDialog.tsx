import { useState } from 'react'

import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  SearchableChecklist,
  useToast,
} from '@/components/ui'
import { useBulkTag } from '@/lib/transactions/queries'
import type { Tag, Uuid } from '@/lib/transactions/types'

import { TagSwatch } from './Pickers'

export type TagRowsMode = 'add' | 'remove'

/**
 * "Add tags" and "Remove tags" for one row or a whole selection. Adding keeps
 * every tag a row already has; removing takes only the ticked ones off.
 */
export function TagRowsDialog({
  mode,
  ids,
  tags,
  onClose,
  onDone,
}: {
  mode: TagRowsMode
  /** The rows to change. Mount the dialog only while it is open: its ticks start empty each time. */
  ids: readonly Uuid[]
  tags: readonly Tag[]
  onClose: () => void
  /** Fired once the rows are changed, so the page can clear its selection. */
  onDone: () => void
}) {
  const { show } = useToast()
  const bulk = useBulkTag()
  const [chosen, setChosen] = useState<string[]>([])
  const count = ids.length

  const rows = `${count} ${count === 1 ? 'row' : 'rows'}`
  const adding = mode === 'add'

  const apply = () => {
    bulk.mutate(
      { ids, ...(adding ? { add: chosen } : { remove: chosen }) },
      {
        onSuccess: () => {
          onDone()
          onClose()
          show({
            title: adding ? `Tagged ${rows}` : `Removed tags from ${rows}`,
            description: chosen
              .map((id) => tags.find((tag) => tag.id === id)?.name ?? 'Unknown tag')
              .join(', '),
          })
        },
      },
    )
  }

  return (
    <Dialog open={count > 0} onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        title={adding ? `Add tags to ${rows}` : `Remove tags from ${rows}`}
        description={
          adding
            ? 'Each row keeps the tags it already has.'
            : 'Rows without a ticked tag are left as they are.'
        }
        footer={
          <DialogActions onCancel={onClose} cancelDisabled={bulk.isPending}>
            <Button
              variant="primary"
              disabled={chosen.length === 0 || bulk.isPending}
              onClick={apply}
            >
              {adding ? 'Add tags' : 'Remove tags'}
            </Button>
          </DialogActions>
        }
      >
        <SearchableChecklist
          options={tags.map((tag) => ({
            id: tag.id,
            text: tag.name,
            label: (
              <span className="row row--wrap txn-actions">
                <TagSwatch color={tag.color} /> {tag.name}
              </span>
            ),
          }))}
          chosen={chosen}
          onChange={setChosen}
          searchLabel="Search tags"
          empty="No matching tag."
          className="picker__list"
        />
      </DialogContent>
    </Dialog>
  )
}
