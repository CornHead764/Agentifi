import { ArrowRightLeft, Pencil, Plus, Trash2, Users } from 'lucide-react'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Card,
  NameDialog,
  OverflowMenu,
  PageHeader,
  RowActions,
  Table,
  TableEmptyRow,
  Td,
  Th,
} from '@/components/ui'
import { useActiveSpace } from '@/contexts/space'
import {
  ROLE_LABELS,
  useCreateSpace,
  useCurrentSpace,
  useRenameSpace,
  useSpaces,
  type Space,
} from '@/lib/clients/spaces'

import { DeleteSpaceDialog } from './spaces/DeleteSpaceDialog'
import { PeopleCard } from './spaces/PeopleCard'

/**
 * Spaces and sharing. Which space is Active comes from the server's answer,
 * never from this page's choice. The people list follows the row picked here,
 * since membership is addressed by space id. Permissions are not re-derived:
 * `is_owner` and `isLastOwner` are the answers the API refuses writes with.
 */
export function SpacesSettings() {
  const spaces = useSpaces()
  const current = useCurrentSpace()
  const [pickedId, setPickedId] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [renaming, setRenaming] = useState<Space | null>(null)
  const [deleting, setDeleting] = useState<Space | null>(null)

  const create = useCreateSpace()
  const rename = useRenameSpace()

  const { setActiveSpace } = useActiveSpace()
  const navigate = useNavigate()
  const switchTo = (space: Space) => {
    setActiveSpace(space.id)
    // Everything cached and on screen belongs to the space being left.
    navigate('/')
  }

  const rows = spaces.data ?? []
  const activeId = current.data?.id ?? null
  const picked =
    rows.find((one) => one.id === pickedId) ??
    rows.find((one) => one.id === activeId) ??
    rows[0] ??
    null

  return (
    <>
      <PageHeader
        title="Spaces & sharing"
        subtitle="Separate sets of accounts, budgets and history. Every screen shows the active one."
        actions={
          <Button variant="primary" size="sm" onClick={() => setCreating(true)}>
            <Plus size={13} /> New space
          </Button>
        }
      />

      <Card title="Spaces" flush>
        <QueryBoundary query={spaces} rows={3}>
          {(listed) => (
            <Table density="sm" lines={2} stack>
              <thead>
                <tr>
                  <Th>Space</Th>
                  <Th>Your role</Th>
                  <Th>Currency</Th>
                  <Th />
                </tr>
              </thead>
              <tbody>
                {listed.length === 0 ? (
                  <TableEmptyRow colSpan={4}>
                    You are not in a space yet. Creating one makes you its owner.
                  </TableEmptyRow>
                ) : null}
                {listed.map((space) => (
                  <tr key={space.id} data-selected={space.id === picked?.id}>
                    <Td label="">
                      <span className="series__name">
                        <span className="cell__clip cell__clip--wide" title={space.name}>
                          {space.name}
                        </span>
                        {space.id === activeId ? <Badge tone="accent">Active</Badge> : null}
                      </span>
                      <span className="cell__sub cell__clip cell__clip--wide">
                        {space.id === activeId
                          ? 'Every other screen is reading this space.'
                          : 'No screen is reading this space.'}
                      </span>
                    </Td>
                    <Td className="stack-inline">{ROLE_LABELS[space.role]}</Td>
                    <Td className="stack-inline">{space.primary_currency}</Td>
                    <Td numeric label="">
                      <RowActions>
                        {space.id === activeId ? null : (
                          <Button size="sm" onClick={() => switchTo(space)}>
                            <ArrowRightLeft size={13} /> Switch
                          </Button>
                        )}
                        <OverflowMenu
                          label={`Actions for ${space.name}`}
                          actions={[
                            {
                              label: 'Show people',
                              icon: <Users size={14} />,
                              onSelect: () => setPickedId(space.id),
                            },
                            space.is_owner && {
                              label: 'Rename space',
                              icon: <Pencil size={14} />,
                              onSelect: () => setRenaming(space),
                            },
                            space.role === 'owner' && {
                              label: 'Delete space…',
                              icon: <Trash2 size={14} />,
                              danger: true,
                              onSelect: () => setDeleting(space),
                            },
                          ]}
                        />
                      </RowActions>
                    </Td>
                  </tr>
                ))}
              </tbody>
            </Table>
          )}
        </QueryBoundary>
      </Card>

      <PeopleCard space={picked} />

      <NameDialog
        title="New space"
        submitLabel="Create space"
        description="A separate set of finances. You will be its owner."
        placeholder="Household"
        open={creating}
        onOpenChange={setCreating}
        onSubmit={(name) => create.mutateAsync(name)}
      />

      <DeleteSpaceDialog
        space={deleting}
        spaces={rows}
        onOpenChange={(open) => (open ? undefined : setDeleting(null))}
        onDeleted={(deleted, next) => {
          setDeleting(null)
          setPickedId(null)
          if (deleted.id === activeId) switchTo(next)
        }}
      />

      <NameDialog
        title="Rename space"
        submitLabel="Save name"
        placeholder="Household"
        initial={renaming?.name ?? ''}
        open={renaming !== null}
        onOpenChange={(open) => (open ? undefined : setRenaming(null))}
        onSubmit={(name) =>
          renaming === null ? Promise.resolve() : rename.mutateAsync({ id: renaming.id, name })
        }
      />
    </>
  )
}
