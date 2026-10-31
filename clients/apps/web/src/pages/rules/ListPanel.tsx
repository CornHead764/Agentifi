import { NotebookPen, Wand2 } from 'lucide-react'
import type { ReactNode } from 'react'

import type { QueryLike } from '@/components/QueryBoundary'
import {
  Card,
  ConfirmDialog,
  EmptyState,
  useConfirm,
  type Confirmable,
  type OverflowAction,
} from '@/components/ui'
import type { RuleCondition } from '@/lib/clients/rules'
import { useCategories, useTags } from '@/lib/transactions/queries'
import type { Uuid } from '@/lib/transactions/types'

import { RuleList } from './RuleList'
import { movedOrder } from './order'

/** What each half of the Rules screen says; the rest of the panel is shared. */
const KINDS = {
  rule: {
    noun: 'rule',
    title: 'Rules',
    subtitle: 'Run top to bottom on new transactions. The first rule to set a field wins.',
    thenHeading: 'Then',
    lines: 1,
    empty: (
      <EmptyState
        icon={<Wand2 size={20} />}
        title="No rules yet"
        body="Match your bank's wording to rename the payee, set the category or add a tag."
      />
    ),
    note: 'Switching off or deleting a rule only stops it going forward. Past changes stay.',
    deleted: 'It stops firing on new transactions. Past changes stay.',
  },
  guidance: {
    noun: 'guidance',
    title: (
      <>
        <NotebookPen size={16} aria-hidden="true" /> Guidance
      </>
    ),
    subtitle:
      'Standing instructions for the assistant, read only on the transactions each one matches.',
    thenHeading: 'Tell the assistant',
    lines: 2,
    empty: (
      <EmptyState
        icon={<NotebookPen size={20} />}
        title="No guidance yet"
        body="For example: “At Fuel Stop, under $20 is food and over $20 is fuel.”"
      />
    ),
    note:
      'Used by the category automations (per-transaction check and daily sweep). A row a note ' +
      'covers always goes to the model, even when its history looks settled.',
    deleted:
      'The assistant stops being told this; past proposals and changes stay. To pause it, switch it off instead.',
  },
} as const

interface Listed {
  id: Uuid
  name: string
  is_active: boolean
  filter: { items: readonly RuleCondition[] }
}

/**
 * One half of the Rules screen: an ordered list of filters with a
 * consequence, and the move, switch and delete controls both halves share.
 * The editors are the caller's, rendered as `children`; the page header holds
 * the button that opens the new one.
 */
export function ListPanel<T extends Listed>({
  kind,
  query,
  rows,
  then,
  menu,
  linkedId,
  reorder,
  setActive,
  remove,
  onEdit,
  children,
}: {
  kind: keyof typeof KINDS
  query: QueryLike<unknown>
  rows: readonly T[]
  then: (row: T) => ReactNode
  /** Menu items between Edit and Delete. */
  menu?: (row: T) => readonly OverflowAction[]
  linkedId?: string | null
  reorder: { isPending: boolean; mutate: (order: Uuid[]) => void }
  setActive: { mutate: (change: { id: Uuid; active: boolean }) => void }
  remove: Confirmable<Uuid>
  onEdit: (row: T) => void
  children?: ReactNode
}) {
  const copy = KINDS[kind]
  const categories = useCategories()
  const tags = useTags()
  const deleting = useConfirm(remove, { variables: (row: T) => row.id })

  // The categories and tags a stored facet's ids are read against; see
  // `readConditions`.
  const universe = { categories: categories.data ?? [], tags: tags.data ?? [] }
  const find = (id: string) => rows.find((row) => row.id === id) ?? null

  return (
    <>
      <Card title={copy.title} subtitle={copy.subtitle} flush>
        <RuleList
          noun={copy.noun}
          askKind={kind}
          thenHeading={copy.thenHeading}
          universe={universe}
          query={query}
          reordering={reorder.isPending}
          linkedId={linkedId}
          entries={rows.map((row) => ({
            id: row.id,
            name: row.name,
            items: row.filter.items,
            active: row.is_active,
            then: then(row),
            menu: menu?.(row),
          }))}
          empty={copy.empty}
          lines={copy.lines}
          onMove={(index, by) => {
            const order = movedOrder(rows, index, by)
            if (order !== null) reorder.mutate(order)
          }}
          onEdit={(id) => {
            const row = find(id)
            if (row !== null) onEdit(row)
          }}
          onDelete={(id) => {
            const row = find(id)
            if (row !== null) deleting.ask(row)
          }}
          onActiveChange={(id, active) => setActive.mutate({ id, active })}
        />
      </Card>

      <p className="hint rule-note">{copy.note}</p>

      {children}

      <ConfirmDialog
        {...deleting.dialog}
        title={deleting.target ? `Delete ${deleting.target.name}?` : `Delete ${copy.noun}?`}
        description={copy.deleted}
        confirmLabel={`Delete ${copy.noun}`}
      />
    </>
  )
}
