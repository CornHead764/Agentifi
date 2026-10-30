import { KeyRound, ShieldCheck, UserMinus, UserPlus } from 'lucide-react'
import { useState } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Card,
  Checkbox,
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
  useHeld,
  useToast,
} from '@/components/ui'
import type { CurrentUser } from '@/contexts/auth'
import { MintedPasswordDialog } from '@/pages/settings/MintedPasswordDialog'
import {
  lastAdministrator,
  signInMethods,
  useAddMembership,
  useAdminSpaces,
  useAdminUsers,
  useCreateUser,
  useRemoveMembership,
  useSetUserPassword,
  useUpdateUser,
  type AdminMembership,
  type AdminUser,
} from '@/lib/clients/admin'
import { asRole, ROLE_LABELS, ROLES, type Role } from '@/lib/clients/spaces'
import { chosenPasswordReady, MIN_PASSWORD_LENGTH } from '@/lib/passwords'

import { AddUserDialog } from './AddUserDialog'
import { Clipped, TwoLines } from './cells'

/**
 * Every account on this server: set a password, disable, promote, or move
 * between spaces. Two refusals the API makes are shown as disabled controls:
 * an administrator cannot demote themself, and the last active administrator
 * cannot be demoted. A minted password is shown exactly once.
 */
export function UsersCard({
  me,
  adding,
  onAddingChange,
}: {
  me: CurrentUser
  /** The Add user dialog, opened from the page header. */
  adding: boolean
  onAddingChange: (open: boolean) => void
}) {
  const { show } = useToast()
  const users = useAdminUsers()
  const spaces = useAdminSpaces()

  const [minted, setMinted] = useState<{ email: string; password: string } | null>(null)
  const [resetting, setResetting] = useState<AdminUser | null>(null)
  const [placing, setPlacing] = useState<AdminUser | null>(null)

  const create = useCreateUser()
  const update = useUpdateUser()
  const setPassword = useSetUserPassword()
  const addMembership = useAddMembership()
  const removeMembership = useConfirm(useRemoveMembership(), {
    variables: ({ user, membership }: { user: AdminUser; membership: AdminMembership }) => ({
      userId: user.id,
      id: membership.id,
    }),
  })

  return (
    <>
      <Card
        title="Users"
        subtitle="Every account on this server. No sign-up page; add them here or with `agentifi user add`."
        flush
      >
        <QueryBoundary query={users} rows={3}>
          {(rows) => (
            <Table density="sm" lines={2} stack>
              <thead>
                <tr>
                  <Th>Person</Th>
                  <Th>Sign-in</Th>
                  <Th>Spaces</Th>
                  <Th />
                </tr>
              </thead>
              <tbody>
                {rows.length === 0 ? (
                  <TableEmptyRow colSpan={4}>
                    No accounts yet, which cannot be true — you are signed in to one.
                  </TableEmptyRow>
                ) : null}
                {rows.map((user) => {
                  const last = lastAdministrator(rows, user)
                  const self = user.id === me.id
                  const demoteHint = self
                    ? 'You cannot take your own administrator rights away.'
                    : 'The last administrator. Make somebody else one first.'
                  return (
                    <tr key={user.id}>
                      <Td label="">
                        <span className="admin-user__name">
                          <Clipped text={user.full_name ?? user.email} />
                          {user.is_superuser ? <Badge tone="accent">Admin</Badge> : null}
                          {user.is_active ? null : <Badge tone="warning">Inactive</Badge>}
                        </span>
                        <Clipped text={user.email} sub />
                      </Td>
                      <Td label="Sign-in" className="stack-inline">
                        <Clipped text={signInMethods(user)} />
                        {user.must_change_password ? (
                          <span className="cell__sub">Owes a password change</span>
                        ) : null}
                      </Td>
                      <Td label="Spaces" className="stack-inline">
                        {user.memberships.length === 0 ? (
                          <span className="muted">In no space — they would see nothing.</span>
                        ) : (
                          <TwoLines
                            items={user.memberships.map(
                              (membership) =>
                                `${membership.space_name} · ${ROLE_LABELS[membership.role]}` +
                                (membership.accepted ? '' : ' (invited)'),
                            )}
                          />
                        )}
                      </Td>
                      <Td numeric label="">
                        <RowActions>
                          <OverflowMenu
                            label={`Actions for ${user.email}`}
                            actions={[
                              {
                                label: 'Set a temporary password',
                                icon: <KeyRound size={14} />,
                                onSelect: () => setResetting(user),
                              },
                              { label: 'Add to a space', icon: <UserPlus size={14} />, onSelect: () => setPlacing(user) },
                            ]}
                            sections={[
                              {
                                entries: [
                                  {
                                    label: user.is_superuser ? 'Remove admin rights' : 'Make an administrator',
                                    icon: <ShieldCheck size={14} />,
                                    disabled: user.is_superuser && (self || last),
                                    title: user.is_superuser && (self || last) ? demoteHint : undefined,
                                    onSelect: () => update.mutate({ id: user.id, is_superuser: !user.is_superuser }),
                                  },
                                  {
                                    label: user.is_active ? 'Deactivate' : 'Reactivate',
                                    icon: <UserMinus size={14} />,
                                    disabled: user.is_active && (self || last),
                                    title:
                                      user.is_active && (self || last)
                                        ? self
                                          ? 'You cannot deactivate your own account.'
                                          : demoteHint
                                        : undefined,
                                    onSelect: () => update.mutate({ id: user.id, is_active: !user.is_active }),
                                  },
                                ],
                              },
                              {
                                entries: user.memberships.map((membership) => ({
                                  label: `Remove from ${membership.space_name}`,
                                  onSelect: () => removeMembership.ask({ user, membership }),
                                })),
                              },
                            ]}
                          />
                        </RowActions>
                      </Td>
                    </tr>
                  )
                })}
              </tbody>
            </Table>
          )}
        </QueryBoundary>
      </Card>

      <AddUserDialog
        open={adding}
        spaces={spaces.data ?? []}
        currency={spaces.data?.[0]?.primary_currency ?? 'USD'}
        pending={create.isPending}
        onCancel={() => onAddingChange(false)}
        onConfirm={(form) =>
          create.mutate(form, {
            onSuccess: (created) => {
              onAddingChange(false)
              if (created.temporary_password) {
                setMinted({
                  email: created.user.email,
                  password: created.temporary_password,
                })
              } else {
                show({ title: `${created.user.email} can sign in now`, tone: 'success' })
              }
            },
          })
        }
      />

      <PasswordDialog
        user={resetting}
        pending={setPassword.isPending}
        onCancel={() => setResetting(null)}
        onConfirm={(password, mustChange) => {
          if (!resetting) return
          const email = resetting.email
          setPassword.mutate(
            { id: resetting.id, password, must_change_password: mustChange },
            {
              onSuccess: (result) => {
                setResetting(null)
                if (result.temporary_password) {
                  setMinted({ email, password: result.temporary_password })
                } else {
                  show({ title: `Password set for ${email}`, tone: 'success' })
                }
              },
            },
          )
        }}
      />

      <MintedPasswordDialog minted={minted} onClose={() => setMinted(null)} />

      <PlaceInSpaceDialog
        user={placing}
        spaces={(spaces.data ?? []).filter(
          (space) => !placing?.memberships.some((one) => one.space_id === space.id),
        )}
        pending={addMembership.isPending}
        onCancel={() => setPlacing(null)}
        onConfirm={(spaceId, role) => {
          if (!placing) return
          addMembership.mutate(
            { userId: placing.id, space_id: spaceId, role },
            { onSuccess: () => setPlacing(null) },
          )
        }}
      />

      <ConfirmDialog
        {...removeMembership.dialog}
        title={
          removeMembership.target
            ? `Remove ${removeMembership.target.user.email} from ${removeMembership.target.membership.space_name}?`
            : 'Remove from this space?'
        }
        description="They lose access to the space but keep their account. Nothing in the space is deleted."
        confirmLabel="Remove from space"
      />
    </>
  )
}

interface PasswordProps {
  pending: boolean
  onCancel: () => void
  onConfirm: (password: string, mustChange: boolean) => void
}

function PasswordDialog({ user: openUser, ...props }: PasswordProps & { user: AdminUser | null }) {
  const user = useHeld(openUser)
  return (
    <FormDialog
      open={openUser !== null}
      onOpenChange={(next) => {
        if (!next) props.onCancel()
      }}
    >
      {user === null ? null : <PasswordForm user={user} {...props} />}
    </FormDialog>
  )
}

function PasswordForm({
  user,
  pending,
  onCancel,
  onConfirm,
}: PasswordProps & { user: AdminUser }) {
  const [password, setPassword] = useState('')
  const [generate, setGenerate] = useState(true)
  const [mustChange, setMustChange] = useState(true)
  const ready = chosenPasswordReady(generate, password)

  return (
    <DialogContent
      title={`Set a password for ${user.email}`}
      description="Signs this account out on every device."
      onSubmit={(event) => {
        event.preventDefault()
        if (pending || !ready) return
        onConfirm(generate ? '' : password, mustChange)
      }}
      footer={
        <DialogActions onCancel={onCancel}>
          <Button
            type="submit"
            variant="primary"
            disabled={pending || !ready}
          >
            {pending ? 'Setting…' : 'Set password'}
          </Button>
        </DialogActions>
      }
    >
      <Field label="Password">
        <Checkbox
          checked={generate}
          onCheckedChange={(next) => setGenerate(next === true)}
          label="Generate a temporary one"
        />
      </Field>
      {!generate ? (
        <Field label="New password" hint={`At least ${MIN_PASSWORD_LENGTH} characters.`}>
          <Input
            type="password"
            value={password}
            autoComplete="new-password"
            onChange={(event) => setPassword(event.target.value)}
          />
        </Field>
      ) : null}
      <Field label="First sign-in">
        <Checkbox
          checked={mustChange}
          onCheckedChange={(next) => setMustChange(next === true)}
          label="Must choose their own password before they can read anything"
        />
      </Field>
    </DialogContent>
  )
}

interface PlaceInSpaceProps {
  spaces: { id: string; name: string }[]
  pending: boolean
  onCancel: () => void
  onConfirm: (spaceId: string, role: Role) => void
}

function PlaceInSpaceDialog({
  user: openUser,
  ...props
}: PlaceInSpaceProps & { user: AdminUser | null }) {
  const user = useHeld(openUser)
  return (
    <FormDialog
      open={openUser !== null}
      onOpenChange={(next) => {
        if (!next) props.onCancel()
      }}
    >
      {user === null ? null : <PlaceInSpaceForm user={user} {...props} />}
    </FormDialog>
  )
}

function PlaceInSpaceForm({
  user,
  spaces,
  pending,
  onCancel,
  onConfirm,
}: PlaceInSpaceProps & { user: AdminUser }) {
  const [spaceId, setSpaceId] = useState(spaces[0]?.id ?? '')
  const [role, setRole] = useState<Role>('member')

  return (
    <DialogContent
      title={`Add ${user.email} to a space`}
      description="Added directly, no invitation: they see it on their next sign-in."
      onSubmit={(event) => {
        event.preventDefault()
        if (pending || spaceId === '') return
        onConfirm(spaceId, role)
      }}
      footer={
        <DialogActions onCancel={onCancel}>
          <Button type="submit" variant="primary" disabled={pending || spaceId === ''}>
            {pending ? 'Adding…' : 'Add to space'}
          </Button>
        </DialogActions>
      }
    >
      {spaces.length === 0 ? (
        <p className="muted">They are already in every space on this server.</p>
      ) : (
        <>
          <Field label="Space">
            <OptionSelect
              value={spaceId}
              onValueChange={setSpaceId}
              placeholder="Select space"
              options={spaces.map((space) => ({ value: space.id, label: space.name }))}
            />
          </Field>
          <Field label="Role">
            <OptionSelect
              value={role}
              onValueChange={(value) => setRole(asRole(value))}
              options={ROLES.map((one) => ({ value: one, label: ROLE_LABELS[one] }))}
            />
          </Field>
        </>
      )}
    </DialogContent>
  )
}
