import { QueryBoundary } from '@/components/QueryBoundary'
import { Card, Table, TableEmptyRow, Td, Th } from '@/components/ui'
import { useAdminSpaces } from '@/lib/clients/admin'
import { ROLE_LABELS } from '@/lib/clients/spaces'
import { Clipped, TwoLines } from './cells'

/**
 * Every space on this server and who is in each one. Read-only: changes belong
 * to the owner on the Spaces screen.
 */
export function SpacesCard() {
  const spaces = useAdminSpaces()

  return (
    <Card
      title="Spaces"
      subtitle="Every set of finances on this server. Only the people listed can see one."
      flush
    >
      <QueryBoundary query={spaces} rows={2}>
        {(rows) => (
          <Table density="sm" lines={2} stack>
            <thead>
              <tr>
                <Th>Space</Th>
                <Th>Currency</Th>
                <Th>People</Th>
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 ? (
                <TableEmptyRow colSpan={3}>
                  No spaces yet. The first account made here brings one with it.
                </TableEmptyRow>
              ) : null}
              {rows.map((space) => (
                <tr key={space.id}>
                  <Td label="">
                    <span className="admin-user__name">
                      <Clipped text={space.name} />
                    </span>
                    <span className="cell__sub">{space.timezone}</span>
                  </Td>
                  <Td label="Currency" className="stack-inline">
                    {space.primary_currency}
                  </Td>
                  <Td label="People" className="stack-inline">
                    {space.members.length === 0 ? (
                      // Reachable, and worth saying rather than leaving blank:
                      // a space with nobody in it cannot be opened by anybody.
                      <span className="muted">Nobody — this space is unreachable.</span>
                    ) : (
                      <TwoLines
                        items={space.members.map(
                          (member) =>
                            `${member.full_name ?? member.email} · ${ROLE_LABELS[member.role]}` +
                            (member.accepted ? '' : ' (invited)'),
                        )}
                      />
                    )}
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </QueryBoundary>
    </Card>
  )
}
