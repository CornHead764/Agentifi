import { LogOut, UserMinus, UserPlus } from 'lucide-react'
import { useState } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Card,
  ConfirmDialog,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  Input,
  OptionSelect,
  OverflowMenu,
  RowActions,
  Table,
  TableEmptyRow,
  Td,
  Th,
  useConfirm,
  type Confirm,
} from '@/components/ui'
import { useAuth } from '@/contexts/auth'
import {
  asRole,
  assignableRoles,
  canManage,
  dayOf,
  displayName,
  isLastOwner,
  isPending,
  ROLE_HINTS,
  ROLE_LABELS,
  sortMembers,
  useChangeMemberRole,
  useInviteMember,
  useMembers,
  useRemoveMember,
  type Member,
  type Role,
  type Space,
} from '@/lib/clients/spaces'

/**
 * Who is in one space, and what each may do. The API's refusals are shown as
 * absent controls: a pending invitation grants nothing; only an owner (or an
 * admin, for everyone but the owner) changes membership, though anybody may
 * leave; and the last accepted owner cannot be demoted or removed.
 */
export function PeopleCard({
  space,
}: {
  space: Space | null
}) {
  const { user } = useAuth()
  const members = useMembers(space?.id ?? null)
  const [inviting, setInviting] = useState(false)
  const invite = useInviteMember()
  const changeRole = useChangeMemberRole()
  const remove = useConfirm(useRemoveMember(), {
    variables: ({ spaceId, member }: Removal) => ({ spaceId, membershipId: member.id }),
  })

  if (!space) return null
  const manage = space.is_owner

  return (
    <>
      <Card
        title={`People in ${space.name}`}
        subtitle={
          manage
            ? 'Invite somebody who has a login here. The server administrator creates logins.'
            : 'Only an owner or admin can change who is here.'
        }
        actions={
          manage ? (
            <Button variant="primary" size="sm" onClick={() => setInviting(true)}>
              <UserPlus size={13} /> Invite
            </Button>
          ) : null
        }
        flush
      >
        <QueryBoundary query={members} rows={3}>
          {(rows) => (
            <Table density="sm" lines={2} stack>
              <thead>
                <tr>
                  <Th>Person</Th>
                  <Th>Role</Th>
                  <Th>Status</Th>
                  <Th />
                </tr>
              </thead>
              <tbody>
                {rows.length === 0 ? (
                  <TableEmptyRow colSpan={4}>Nobody is in this space yet.</TableEmptyRow>
                ) : null}
                {sortMembers(rows).map((member) => {
                  const self = member.user_id === user?.id
                  const stranding = isLastOwner(rows, member)
                  const mine = canManage(space, member)
                  return (
                    <tr key={member.id}>
                      <Td label="">
                        <span className="series__name">
                          <span className="cell__clip" title={displayName(member)}>
                            {displayName(member)}
                          </span>
                          {self ? <Badge>You</Badge> : null}
                        </span>
                        <span className="cell__sub cell__clip" title={member.email}>
                          {member.email}
                        </span>
                      </Td>
                      <Td label="">
                        {mine && !stranding ? (
                          <OptionSelect
                            value={member.role}
                            onValueChange={(value) =>
                              changeRole.mutate({
                                spaceId: space.id,
                                membershipId: member.id,
                                role: asRole(value),
                              })
                            }
                            size="sm"
                            aria-label={`Role for ${displayName(member)}`}
                            options={assignableRoles(space).map((role) => ({
                              value: role,
                              label: ROLE_LABELS[role],
                            }))}
                          />
                        ) : (
                          <span>{ROLE_LABELS[member.role]}</span>
                        )}
                        {stranding ? (
                          // `cell__sub` is the line break; `hint`
                          // only colours and shrinks it.
                          <span className="cell__sub cell__clip hint" title={LAST_OWNER}>
                            {LAST_OWNER}
                          </span>
                        ) : null}
                      </Td>
                      <Td label="" className="nowrap">
                        {isPending(member) ? (
                          <>
                            <Badge tone="warning">Invited</Badge>
                            <span
                              className="cell__sub cell__clip"
                              title={`No access until accepted${sentOn(member)}`}
                            >
                              No access until accepted{sentOn(member)}
                            </span>
                          </>
                        ) : (
                          <span className="muted">Joined {dayOf(member.accepted_at)}</span>
                        )}
                      </Td>
                      <Td numeric label="" className="stack-corner">
                        {self || mine ? (
                          <RowActions>
                            <OverflowMenu
                              label={`Actions for ${displayName(member)}`}
                              actions={[
                                {
                                  label: removalLabel(member, self),
                                  icon: self ? <LogOut size={14} /> : <UserMinus size={14} />,
                                  danger: true,
                                  disabled: stranding,
                                  onSelect: () => remove.ask({ spaceId: space.id, member }),
                                },
                              ]}
                            />
                          </RowActions>
                        ) : null}
                      </Td>
                    </tr>
                  )
                })}
              </tbody>
            </Table>
          )}
        </QueryBoundary>
      </Card>

      <InviteDialog
        roles={assignableRoles(space)}
        open={inviting}
        pending={invite.isPending}
        onCancel={() => setInviting(false)}
        onConfirm={(email, role) =>
          invite.mutate({ spaceId: space.id, email, role }, { onSuccess: () => setInviting(false) })
        }
      />

      <RemoveDialog
        space={space}
        confirm={remove}
        self={remove.target?.member.user_id === user?.id}
      />
    </>
  )
}

interface InviteProps {
  roles: readonly Role[]
  pending: boolean
  onCancel: () => void
  onConfirm: (email: string, role: Role) => void
}

function InviteDialog({ open, ...props }: InviteProps & { open: boolean }) {
  return (
    <FormDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) props.onCancel()
      }}
    >
      <InviteForm {...props} />
    </FormDialog>
  )
}

function InviteForm({ roles, pending, onCancel, onConfirm }: InviteProps) {
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('member')

  const submit = () => {
    if (email.trim() === '') return
    onConfirm(email.trim(), role)
  }

  return (
    <DialogContent
      title="Invite somebody"
      description="They need a login here, which the server administrator creates. No access until they accept."
      onSubmit={submit}
      footer={
        <DialogActions onCancel={onCancel}>
          <Button type="submit" variant="primary" disabled={pending || email.trim() === ''}>
            Send invitation
          </Button>
        </DialogActions>
      }
    >
      <Field label="Email or username">
        <Input
          value={email}
          autoCapitalize="none"
          spellCheck={false}
          placeholder="them@example.com"
          onChange={(event) => setEmail(event.target.value)}
        />
      </Field>
      <Field label="Role" hint={ROLE_HINTS[role]}>
        <OptionSelect
          value={role}
          onValueChange={(value) => setRole(asRole(value))}
          options={roles.map((one) => ({ value: one, label: ROLE_LABELS[one] }))}
        />
      </Field>
    </DialogContent>
  )
}

interface Removal {
  spaceId: string
  member: Member
}

/**
 * Removal, said plainly: revoking an invitation, taking access away, or
 * leaving. Only leaving needs somebody else to undo.
 */
function RemoveDialog({
  space,
  confirm,
  self,
}: {
  space: Space
  confirm: Confirm<Removal>
  self: boolean
}) {
  const member = confirm.target?.member
  if (member === undefined) return null
  return (
    <ConfirmDialog
      {...confirm.dialog}
      title={
        self
          ? `Leave ${space.name}?`
          : isPending(member)
            ? `Withdraw the invitation for ${displayName(member)}?`
            : `Remove ${displayName(member)} from ${space.name}?`
      }
      confirmLabel={removalLabel(member, self)}
    >
      {self ? (
        <p>
          You lose access to every account, transaction and plan in this space. An owner has to
          invite you back.
        </p>
      ) : isPending(member) ? (
        <p>The invitation disappears. Nothing changes for them — it granted no access.</p>
      ) : (
        <p>They lose access immediately.</p>
      )}
      <p className="muted">
        Nothing they added to the space is deleted.
      </p>
    </ConfirmDialog>
  )
}

const LAST_OWNER = 'The last owner. Make somebody else an owner first.'

function sentOn(member: Member): string {
  const day = dayOf(member.invited_at)
  return day ? ` · sent ${day}` : ''
}

function removalLabel(member: Member, self: boolean): string {
  if (self) return 'Leave space'
  return isPending(member) ? 'Withdraw invitation' : 'Remove from space'
}
