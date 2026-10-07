import { clsx } from 'clsx'
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  ChevronRight,
  FolderTree,
  Pencil,
  Plus,
  Sparkles,
  Trash2,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'

import { IMPORT_PATH } from '@/components/onboarding/paths'
import {
  Badge,
  Button,
  Card,
  EmptyState,
  IconButton,
  OverflowMenu,
  PageHeader,
  RowActions,
  SearchInput,
  SkeletonRows,
  Table,
  TableEmptyRow,
  Td,
  Th,
  Tooltip,
  useToast,
} from '@/components/ui'
import {
  useAdoptDefaultCategories,
  useCreateCategory,
  useDeleteCategory,
  useUpdateCategory,
  type CategoryWrite,
} from '@/lib/clients/categories'
import { useCategories } from '@/lib/transactions/queries'
import {
  buildCategoryTree,
  flattenCategoryTree,
  searchCategoryTree,
  type CategoryNode,
} from '@/lib/categoryTree'
import type { Category, Uuid } from '@/lib/transactions/types'

import { CategoryDeleteDialog } from './categories/CategoryDeleteDialog'
import { CategoryEditor } from './categories/CategoryEditor'
import { TagsCard } from './categories/TagsCard'
import { CleanupUnusedDialog } from './categories/CleanupUnusedDialog'
import { kindLabel } from './categories/kinds'
import { MAX_DEPTH, findCategoryNode, moveWithinSiblings } from './categories/tree'

import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { categorySubject } from '@/lib/assistant/subjects'
import { useLinkedItem, useScrollToLinked } from '@/lib/linkedItem'
import { toggledSet } from '@/lib/toggle'

/** Which dialog is open, and what it is about to write. */
type Editing = { mode: 'create'; parentId: Uuid | null } | { mode: 'edit'; category: Category }

/**
 * Categories and tags.
 *
 * A system category (Opening Balance, Balance Adjustment) cannot be renamed,
 * re-parented or deleted: the API refuses the whole `PATCH`, so those rows get
 * a badge and no menu. A category a process finds by `known_category_id`
 * (Transfer, Credit Card Payment, Fees & Charges) can be renamed and
 * re-parented but keeps its kind and cannot be deleted; `protected_reason` says
 * why.
 *
 * The two exclusions are independent, and there is deliberately no single
 * "exclude" control.
 */
export function CategoriesTagsSettings() {
  const { show } = useToast()

  const categories = useCategories()
  const create = useCreateCategory()
  const update = useUpdateCategory()
  const remove = useDeleteCategory()
  const adopt = useAdoptDefaultCategories()

  const [query, setQuery] = useState('')
  const [collapsed, setCollapsed] = useState<ReadonlySet<Uuid>>(new Set())
  const [editing, setEditing] = useState<Editing | null>(null)
  const [deleting, setDeleting] = useState<Uuid | null>(null)
  const [cleaning, setCleaning] = useState(false)

  // `?category=<id>`: an accepted assistant card leads here, to the row it made.
  const { linked } = useLinkedItem('category')
  useScrollToLinked(linked, categories.isSuccess)

  const tree = useMemo(() => buildCategoryTree(categories.data ?? []), [categories.data])
  const searching = query.trim() !== ''
  const rows = useMemo(() => {
    // A search overrides the collapse state: a match nobody can see is not a
    // result, and re-expanding a branch to find it is the work being avoided.
    const matched = searchCategoryTree(tree, query)
    return flattenCategoryTree(matched, searching ? new Set() : collapsed)
  }, [collapsed, query, searching, tree])

  const toggle = (id: Uuid) => setCollapsed((current) => toggledSet(current, id))

  const doomed = deleting === null ? null : findCategoryNode(tree, deleting)

  // A move renumbers one level. A search disables it: the filtered rows have
  // no meaningful "up".
  const move = (id: Uuid, direction: -1 | 1) => {
    for (const patch of moveWithinSiblings(tree, id, direction)) {
      update.mutate({ id: patch.id, patch: { sort_order: patch.sort_order } })
    }
  }

  return (
    <>
      <PageHeader
        title="Categories & tags"
        actions={
          <Button
            variant="primary"
            size="sm"
            onClick={() => setEditing({ mode: 'create', parentId: null })}
          >
            <Plus size={13} /> New category
          </Button>
        }
      />

      <TagsCard />

      <Card
        title="Categories"
        subtitle="Group, category, subcategory. Type sets its spending plan bucket."
        actions={
          <>
            <SearchInput
              size="sm"
              className="cat-search"
              value={query}
              onChange={setQuery}
              placeholder="Search categories"
              aria-label="Search categories"
            />
            <Button size="sm" onClick={() => setCleaning(true)}>
              <Sparkles size={13} aria-hidden="true" /> Clean up unused
            </Button>
          </>
        }
        flush={rows.length > 0}
      >
        {categories.isPending ? <SkeletonRows rows={6} /> : null}
        {categories.isSuccess && tree.length === 0 ? (
          <EmptyState
            icon={<FolderTree size={20} />}
            title="No categories yet"
            body="Start with the standard set, add your own, or import a Simplifi CSV to bring its categories across."
            action={
              <div className="empty__actions">
                <Button
                  variant="primary"
                  disabled={adopt.isPending}
                  onClick={() =>
                    adopt.mutate(undefined, {
                      onSuccess: (result) =>
                        show({ title: `Added ${result.created} categories.`, tone: 'success' }),
                    })
                  }
                >
                  <FolderTree size={14} /> Start with the standard set
                </Button>
                <Button asChild>
                  <Link to={IMPORT_PATH}>Import a Simplifi export</Link>
                </Button>
              </div>
            }
          />
        ) : null}

        {rows.length > 0 || (searching && tree.length > 0) ? (
          <Table density="sm">
            <thead>
              <tr>
                <Th>Name</Th>
                <Th>Type</Th>
                <Th className="hide-narrow">Tax form &amp; line item</Th>
                <Th aria-label="Actions" />
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 ? (
                <TableEmptyRow colSpan={4}>No category matches “{query.trim()}”.</TableEmptyRow>
              ) : null}
              {rows.map((node) => (
                <CategoryRow
                  key={node.category.id}
                  node={node}
                  linked={node.category.id === linked}
                  collapsed={collapsed.has(node.category.id)}
                  collapsible={!searching && node.children.length > 0}
                  onToggle={() => toggle(node.category.id)}
                  onEdit={() => setEditing({ mode: 'edit', category: node.category })}
                  onAddChild={() =>
                    setEditing({ mode: 'create', parentId: node.category.id })
                  }
                  onExclude={(patch) => update.mutate({ id: node.category.id, patch })}
                  onDelete={() => setDeleting(node.category.id)}
                  onMove={
                    searching
                      ? undefined
                      : (direction) => move(node.category.id, direction)
                  }
                  canMoveUp={
                    !searching && moveWithinSiblings(tree, node.category.id, -1).length > 0
                  }
                  canMoveDown={
                    !searching && moveWithinSiblings(tree, node.category.id, 1).length > 0
                  }
                />
              ))}
            </tbody>
          </Table>
        ) : null}
      </Card>

      {cleaning ? (
        <CleanupUnusedDialog onClose={() => setCleaning(false)} />
      ) : null}

      {editing !== null ? (
        <CategoryEditor
          tree={tree}
          category={editing.mode === 'edit' ? editing.category : null}
          parentId={editing.mode === 'create' ? editing.parentId : null}
          pending={create.isPending || update.isPending}
          onSubmit={(body) =>
            editing.mode === 'edit'
              ? update.mutateAsync({ id: editing.category.id, patch: body })
              : create.mutateAsync(body)
          }
          onClose={() => setEditing(null)}
        />
      ) : null}

      {doomed !== null ? (
        <CategoryDeleteDialog
          node={doomed}
          pending={remove.isPending}
          onConfirm={() => {
            remove.mutate(doomed.category.id)
            setDeleting(null)
          }}
          onClose={() => setDeleting(null)}
        />
      ) : null}
    </>
  )
}

function CategoryRow({
  node,
  linked,
  collapsed,
  collapsible,
  onToggle,
  onEdit,
  onAddChild,
  onExclude,
  onDelete,
  onMove,
  canMoveUp,
  canMoveDown,
}: {
  node: CategoryNode<Category>
  /** The row a link pointed at. */
  linked: boolean
  collapsed: boolean
  collapsible: boolean
  onToggle: () => void
  onEdit: () => void
  onAddChild: () => void
  onExclude: (patch: CategoryWrite) => void
  onDelete: () => void
  /** Absent while a search is on, where the rows are not the whole level. */
  onMove?: (direction: -1 | 1) => void
  canMoveUp: boolean
  canMoveDown: boolean
}) {
  const { category, depth } = node

  return (
    <tr data-linked={linked ? 'true' : undefined}>
      <Td>
        <span className={clsx('cat-name', `cat-name--${depth}`)}>
          {collapsible ? (
            <IconButton
              label={`${collapsed ? 'Expand' : 'Collapse'} ${category.name}`}
              variant="ghost"
              size="sm"
              onClick={onToggle}
            >
              {collapsed ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
            </IconButton>
          ) : (
            <span className="cat-name__indent" aria-hidden="true" />
          )}
          <span className="cat-name__text" title={category.name}>
            {category.name}
          </span>
          <CategoryFlags category={category} />
        </span>
      </Td>
      <Td>{kindLabel(category.kind)}</Td>
      {/* A tax line is read once a year on a laptop; on a phone it is three
          columns of tree in the way of the name. */}
      <Td className="hide-narrow">{taxLabel(category)}</Td>
      <Td numeric>
        {category.is_editable ? (
          <RowActions>
            <CategoryMenu
              category={category}
              canHoldChildren={depth + 1 <= MAX_DEPTH - 1}
              onEdit={onEdit}
              onAddChild={onAddChild}
              onExclude={onExclude}
              onDelete={onDelete}
              onMove={onMove}
              canMoveUp={canMoveUp}
              canMoveDown={canMoveDown}
            />
          </RowActions>
        ) : null}
      </Td>
    </tr>
  )
}

/** The code, until there is a TXF table to render "Schedule A" from. */
function taxLabel(category: Category): string {
  if (category.txf_id === null) return '—'
  const extra = category.txf_ids.filter((code) => code !== category.txf_id).length
  return extra === 0 ? category.txf_id : `${category.txf_id} +${extra}`
}

function CategoryFlags({ category }: { category: Category }) {
  const flags: string[] = []
  if (category.excluded_from_reports) flags.push('Not in reports')
  if (category.excluded_from_spending_plan) flags.push('Not in the plan')
  if (category.excluded_from_category_list) flags.push('Hidden')

  return (
    <span className="chips chips--tight cat-name__flags">
      {category.is_editable ? null : (
        <Tooltip
          side="top"
          label="Maintained by the app; cannot be renamed, moved or deleted."
        >
          <Badge tone="accent">System</Badge>
        </Tooltip>
      )}
      {/* One badge whatever the count, so an excluded category's row is no
          taller than the rest; the tooltip names every flag. */}
      {flags.length === 1 ? <Badge>{flags[0]}</Badge> : null}
      {flags.length > 1 ? (
        <Tooltip side="top" label={flags.join(', ')}>
          <Badge>
            {flags[0]} +{flags.length - 1}
          </Badge>
        </Tooltip>
      ) : null}
    </span>
  )
}

function CategoryMenu({
  category,
  canHoldChildren,
  onEdit,
  onAddChild,
  onExclude,
  onDelete,
  onMove,
  canMoveUp,
  canMoveDown,
}: {
  category: Category
  canHoldChildren: boolean
  onEdit: () => void
  onAddChild: () => void
  onExclude: (patch: CategoryWrite) => void
  onDelete: () => void
  onMove?: (direction: -1 | 1) => void
  canMoveUp: boolean
  canMoveDown: boolean
}) {
  return (
    <OverflowMenu
      label={`Actions for ${category.name}`}
      actions={[
        { label: 'Edit category', icon: <Pencil size={14} />, onSelect: onEdit },
        <AskMenuItem subject={() => categorySubject(category)} />,
        canHoldChildren && {
          label: 'Add a subcategory',
          icon: <Plus size={14} />,
          onSelect: onAddChild,
        },
        // Imported categories share one `sort_order`, so a move renumbers the
        // whole level.
        onMove && {
          label: 'Move up',
          icon: <ArrowUp size={14} />,
          disabled: !canMoveUp,
          onSelect: () => onMove(-1),
        },
        onMove && {
          label: 'Move down',
          icon: <ArrowDown size={14} />,
          disabled: !canMoveDown,
          onSelect: () => onMove(1),
        },
        {
          label: 'Delete category',
          icon: <Trash2 size={14} />,
          danger: true,
          disabled: category.protected_reason !== null,
          title:
            category.protected_reason === null
              ? undefined
              : `Cannot be deleted because ${category.protected_reason}.`,
          onSelect: onDelete,
        },
      ]}
      sections={[
        {
          label: 'Exclude from',
          // Three separate questions, three separate flags. Setting one must not
          // move another — see the exclusion columns in the schema.
          entries: [
            {
              label: 'Reports',
              checked: category.excluded_from_reports,
              onCheckedChange: (checked) => onExclude({ excluded_from_reports: checked }),
            },
            {
              label: 'Spending Plan',
              checked: category.excluded_from_spending_plan,
              onCheckedChange: (checked) => onExclude({ excluded_from_spending_plan: checked }),
            },
            {
              label: 'The category picker',
              checked: category.excluded_from_category_list,
              onCheckedChange: (checked) => onExclude({ excluded_from_category_list: checked }),
            },
          ],
        },
      ]}
    />
  )
}
