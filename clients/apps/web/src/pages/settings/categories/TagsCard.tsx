import { Pencil, Plus, Tag as TagIcon, Trash2 } from 'lucide-react'
import { useState } from 'react'

import {
  Button,
  Card,
  type Confirm,
  ConfirmDialog,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  EmptyState,
  IconButton,
  NameDialog,
  SkeletonRows,
  useConfirm,
} from '@/components/ui'
import { TagSwatch } from '@/components/transactions/Pickers'
import { useCreateTag, useDeleteTag, useTagUsage, useUpdateTag } from '@/lib/clients/categories'
import { useTags } from '@/lib/transactions/queries'
import type { Tag } from '@/lib/transactions/types'

import { tagDeleteWarning } from './warnings'
import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { tagSubject } from '@/lib/assistant/subjects'

/**
 * The chart palette from `tokens.css`, as literals: a tag's colour is stored
 * on the row as data, not as a CSS variable name.
 */
const TAG_COLORS: readonly string[] = [
  '#6c5ce7',
  '#22d3ee',
  '#3ddc84',
  '#ffb547',
  '#ff6b6b',
  '#c084fc',
  '#60a5fa',
  '#f472b6',
]

/**
 * Tags, as a flat chip row. Deleting one unlinks it from every transaction and
 * split first, so undeleting would not bring the links back.
 */
export function TagsCard() {
  const tags = useTags()
  const create = useCreateTag()
  const rename = useUpdateTag()
  const remove = useConfirm(useDeleteTag(), {
    variables: (tag: Tag) => tag.id,
  })

  const [naming, setNaming] = useState<Tag | 'new' | null>(null)

  const rows = [...(tags.data ?? [])].sort((left, right) =>
    left.name.localeCompare(right.name, undefined, { sensitivity: 'base' }),
  )

  return (
    <Card
      title="Tags"
      subtitle="Any number per row, across categories."
      actions={
        <Button variant="primary" size="sm" onClick={() => setNaming('new')}>
          <Plus size={13} /> New tag
        </Button>
      }
    >
      {tags.isPending ? <SkeletonRows rows={1} /> : null}
      {tags.isSuccess && rows.length === 0 ? (
        <EmptyState
          icon={<TagIcon size={20} />}
          title="No tags yet"
          body="E.g. Reimbursable or Vacation. Add one here or from any transaction."
        />
      ) : null}
      {rows.length > 0 ? (
        <div className="chips chips--wrap">
          {rows.map((tag) => (
            <DropdownMenu key={tag.id}>
              <DropdownMenuTrigger asChild>
                <button type="button" className="chip tag-chip">
                  <TagSwatch color={tag.color} />
                  {tag.name}
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start">
                <DropdownMenuItem icon={<Pencil size={14} />} onSelect={() => setNaming(tag)}>
                  Rename or recolor
                </DropdownMenuItem>
                <AskMenuItem subject={() => tagSubject(tag)} />
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  icon={<Trash2 size={14} />}
                  danger
                  onSelect={() => remove.ask(tag)}
                >
                  Delete tag
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ))}
        </div>
      ) : null}

      {naming !== null ? (
        <TagNameDialog
          tag={naming === 'new' ? null : naming}
          onSubmit={(name, color) =>
            naming === 'new'
              ? create.mutateAsync({ name, color })
              : rename.mutateAsync({ id: naming.id, patch: { name, color } })
          }
          onClose={() => setNaming(null)}
        />
      ) : null}

      <TagDeleteDialog confirm={remove} />
    </Card>
  )
}

function TagNameDialog({
  tag,
  onSubmit,
  onClose,
}: {
  tag: Tag | null
  onSubmit: (name: string, color: string | null) => Promise<unknown>
  onClose: () => void
}) {
  const [color, setColor] = useState<string | null>(tag?.color ?? null)

  return (
    <NameDialog
      open
      onOpenChange={(next) => (next ? undefined : onClose())}
      title={tag === null ? 'New tag' : `Edit ${tag.name}`}
      description="A rename and a recolor both reach every transaction that carries the tag."
      placeholder="Reimbursable"
      initial={tag?.name ?? ''}
      submitLabel={tag === null ? 'Create tag' : 'Save'}
      onSubmit={(name) => onSubmit(name, color)}
    >
      {/* Not a `Field`: a field hands its one control id to everything inside
          it, and nine swatches sharing an id is nine labels on one button. */}
      <div className="field" role="group" aria-label="Color">
        <span className="field__label">Color</span>
        <div className="chips chips--wrap">
          <IconButton
            size="sm"
            variant="ghost"
            label="No color"
            aria-pressed={color === null}
            onClick={() => setColor(null)}
          >
            <span className="tag-swatch" />
          </IconButton>
          {TAG_COLORS.map((choice) => (
            <IconButton
              key={choice}
              size="sm"
              variant="ghost"
              label={`Color ${choice}`}
              aria-pressed={color === choice}
              onClick={() => setColor(choice)}
            >
              <span className="tag-swatch" style={{ background: choice }} />
            </IconButton>
          ))}
        </div>
      </div>
    </NameDialog>
  )
}

/** A failed count says so, because silence reads as "nothing carries it". */
function TagDeleteDialog({ confirm }: { confirm: Confirm<Tag> }) {
  const tag = confirm.target
  const usage = useTagUsage(tag?.id ?? null)

  return (
    <ConfirmDialog
      {...confirm.dialog}
      title={tag ? `Delete ${tag.name}?` : 'Delete this tag?'}
      description="Removed from every transaction and split that carries it."
      confirmLabel="Delete tag"
    >
      <p className="hint">
        {tagDeleteWarning({ isError: usage.isError, count: usage.data })}
      </p>
    </ConfirmDialog>
  )
}
