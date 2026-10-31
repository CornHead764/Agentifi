import { Sparkles } from 'lucide-react'
import { useRef, useState, type ReactNode } from 'react'

import {
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  EmptyState,
  SkeletonRows,
  useToast,
} from '@/components/ui'
import { TagSwatch } from '@/components/transactions/Pickers'
import { usePurgeUnused, useUnused, type UnusedList } from '@/lib/clients/categories'
import { clickRange } from '@/lib/selection'
import type { Uuid } from '@/lib/transactions/types'

import {
  checkedAgainst,
  chosenPhrase,
  counts,
  everything,
  purgeOutcome,
  purgeRequest,
  sectionState,
  setSection,
  toggle,
  type SectionCount,
  type Selection,
} from './cleanup'
import { kindLabel } from './kinds'

/**
 * Every unused category and tag, ticked; the ticked list is the confirmation.
 * The list is advice: the server re-checks inside the delete's transaction and
 * refuses the whole request if any has been used since.
 */
export function CleanupUnusedDialog({
  onClose,
}: {
  onClose: () => void
}) {
  const { show } = useToast()
  const unused = useUnused(true)
  const purge = usePurgeUnused()
  // Null until the list arrives, and then everything on it.
  const [chosen, setChosen] = useState<Selection | null>(null)

  const list = unused.data
  const selection = chosen ?? (list ? everything(list) : new Set<Uuid>())
  const tally = list ? counts(list, selection) : null
  const picked = {
    categories: tally?.categories.chosen ?? 0,
    tags: tally?.tags.chosen ?? 0,
  }
  const nothingPicked = picked.categories + picked.tags === 0

  const deleteThem = () => {
    if (!list) return
    purge.mutate(purgeRequest(list, selection), {
      onSuccess: (result) => {
        show({ ...purgeOutcome(result), tone: 'success' })
        onClose()
      },
      onError: () => {
        setChosen(null)
        void unused.refetch()
      },
    })
  }

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        wide
        title="Clean up unused"
        description="Categories and tags that no reviewed transaction, and nothing else in your history, refers to. Unreviewed transactions do not count: a deleted tag comes off them, and one whose category an automation filed is uncategorized and asked about again. Everything is ticked; untick what you want to keep. This screen cannot bring them back."
        footer={
          <DialogActions onCancel={onClose} cancelDisabled={purge.isPending}>
            <Button
              variant="danger"
              onClick={deleteThem}
              disabled={!list || nothingPicked || purge.isPending}
            >
              {nothingPicked ? 'Delete' : `Delete ${chosenPhrase(picked)}`}
            </Button>
          </DialogActions>
        }
      >
        {unused.isPending ? <SkeletonRows rows={5} /> : null}
        {unused.isError ? (
          <p className="hint">
            The unused categories and tags could not be read just now. Nothing was deleted.
          </p>
        ) : null}
        {list ? (
          <CleanupChoices list={list} selection={selection} onChange={(next) => setChosen(next)} />
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

/**
 * The two ticked lists. Separate from the dialog so it renders without one,
 * which is how its markup is tested.
 */
export function CleanupChoices({
  list,
  selection,
  onChange,
}: {
  list: UnusedList
  selection: Selection
  onChange: (next: Selection) => void
}) {
  const tally = counts(list, selection)
  if (tally.categories.total + tally.tags.total === 0) {
    return (
      <>
        <EmptyState
          icon={<Sparkles size={20} />}
          title="Nothing to clean up"
          body="Every category and tag is used by a reviewed transaction or something else in your history."
        />
        <p className="hint">{checkedAgainst(list.categories_checked)}</p>
      </>
    )
  }

  const shared = { list, selection, onChange }
  return (
    <div className="cleanup">
      <CleanupSection
        {...shared}
        kind="categories"
        title="Categories"
        count={tally.categories}
        checked={list.categories_checked}
        rows={list.categories.map((row) => ({
          id: row.id,
          label: (
            <>
              {row.path} <span className="muted">· {kindLabel(row.kind)}</span>
            </>
          ),
        }))}
      />
      <CleanupSection
        {...shared}
        kind="tags"
        title="Tags"
        count={tally.tags}
        checked={list.tags_checked}
        rows={list.tags.map((row) => ({
          id: row.id,
          label: (
            <>
              <TagSwatch color={row.color} /> {row.name}
            </>
          ),
        }))}
      />
    </div>
  )
}

function CleanupSection({
  list,
  selection,
  onChange,
  kind,
  title,
  count,
  checked,
  rows,
}: {
  list: UnusedList
  selection: Selection
  onChange: (next: Selection) => void
  kind: 'categories' | 'tags'
  title: string
  count: SectionCount
  checked: readonly string[]
  rows: { id: Uuid; label: ReactNode }[]
}) {
  const anchor = useRef<Uuid | null>(null)
  if (count.total === 0) return null
  const click = (id: Uuid, extend: boolean) => {
    const drawn = rows.map((row) => row.id)
    const reached = clickRange(drawn, anchor.current, id, extend)
    anchor.current = id
    onChange(toggle(list, selection, id, reached))
  }
  return (
    <section className="cleanup__section" aria-label={`Unused ${kind}`}>
      <Checkbox
        label={`${title} — ${count.chosen} of ${count.total}`}
        checked={sectionState(count)}
        onCheckedChange={() =>
          onChange(setSection(list, selection, kind, count.chosen < count.total))
        }
      />
      <ul className="cleanup__list">
        {rows.map((row) => (
          <li key={row.id}>
            <Checkbox
              label={row.label}
              checked={selection.has(row.id)}
              onClick={(event) => click(row.id, event.shiftKey)}
            />
          </li>
        ))}
      </ul>
      <p className="hint">{checkedAgainst(checked)}</p>
    </section>
  )
}
