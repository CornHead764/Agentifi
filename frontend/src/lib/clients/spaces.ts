/**
 * Spaces and the people in them. The membership rules below mirror the
 * server's so no screen offers a control that comes back a 403 or 409: a
 * pending invitation grants nothing, `space.is_owner` is the server's own
 * `RequireOwner` answer, and a space must keep an accepted owner.
 */

import { useQuery } from '@tanstack/react-query'

import {
  AcceptInvitationResponseSchema,
  CreateSpaceResponseSchema,
  DeleteSpaceRequestSchema,
  DeleteSpaceResponseSchema,
  GetCurrentSpaceResponseSchema,
  GetDashboardLayoutResponseSchema,
  HouseholdService,
  InviteMemberRequestSchema,
  InviteMemberResponseSchema,
  ListInvitationsResponseSchema,
  ListMembersResponseSchema,
  ListSpacesResponseSchema,
  RenameSpaceRequestSchema,
  RenameSpaceResponseSchema,
  SaveDashboardLayoutRequestSchema,
  UpdateMemberRequestSchema,
  UpdateMemberResponseSchema,
  UpdatePreferencesRequestSchema,
  UpdatePreferencesResponseSchema,
} from '@/gen/agentifi/v1/household_pb'
import { formatTimestamp } from '@/lib/format'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import { fromWire, toPatch, toWire } from '@/lib/rpc/wire'
import { rpcClient, unary } from '@/lib/rpcSession'

export type Role = 'owner' | 'admin' | 'member' | 'viewer'

export interface Space {
  id: string
  name: string
  primary_currency: string
  timezone: string
  /** Empty means the built-in default. */
  default_date_range: string
  /** `null` is "all types"; `[]` is "none" — do not normalise one into the other. */
  sidebar_account_types: string[] | null
  role: Role
  can_write: boolean
  /** May administer (true for an admin too): change membership, rename, delete. */
  is_owner: boolean
  /** Never null on a listed space. */
  joined_at: string | null
}

export interface Member {
  id: string
  user_id: string
  email: string
  full_name: string | null
  role: Role
  invited_at: string | null
  /** Null while the invitation is outstanding, which grants nothing. */
  accepted_at: string | null
}

/**
 * Carries the space's name because until it is accepted the space is absent
 * from `useSpaces` and resolving it is a 404.
 */
export interface Invitation {
  /** The membership id. */
  id: string
  space_id: string
  space_name: string
  role: Role
  invited_at: string | null
}

export const ROLE_LABELS: Record<Role, string> = {
  owner: 'Owner',
  admin: 'Admin',
  member: 'Member',
  viewer: 'Viewer',
}

export const ROLE_HINTS: Record<Role, string> = {
  owner: 'Everything, including who else is here.',
  admin: 'Everything except the owner, whose membership only an owner may change.',
  member: 'Can add and edit transactions, budgets and accounts.',
  viewer: 'Can read everything and change nothing.',
}

/** In descending rights. */
export const ROLES: readonly Role[] = ['owner', 'admin', 'member', 'viewer']

/** Falls back to `member`, the least dangerous role, rather than casting. */
export function asRole(value: string): Role {
  return ROLES.find((one) => one === value) ?? 'member'
}

export function isPending(member: Member): boolean {
  return member.accepted_at === null
}

/**
 * Whether this membership is the only accepted owner, which may not be demoted
 * or removed. Neither an admin (cannot make another owner) nor a pending
 * invitation counts as a successor — the server's rule.
 */
export function isLastOwner(members: readonly Member[], member: Member): boolean {
  if (isPending(member) || member.role !== 'owner') return false
  return !members.some((one) => one.id !== member.id && !isPending(one) && one.role === 'owner')
}

/** `is_owner` includes admins, but an owner's membership is not an admin's to change. */
export function canManage(space: Space, member: Member): boolean {
  return space.is_owner && (space.role === 'owner' || member.role !== 'owner')
}

/** Only an owner may make another owner. */
export function assignableRoles(space: Space): readonly Role[] {
  return space.role === 'owner' ? ROLES : ROLES.filter((role) => role !== 'owner')
}

/** Accepted members first, then outstanding invitations, each by name. */
export function sortMembers(members: readonly Member[]): Member[] {
  return [...members].sort((a, b) => {
    if (isPending(a) !== isPending(b)) return isPending(a) ? 1 : -1
    return displayName(a).localeCompare(displayName(b))
  })
}

export function displayName(member: Member): string {
  return member.full_name ?? member.email
}

/** The day of an instant, in the viewer's time zone. */
export function dayOf(iso: string | null): string | null {
  return iso ? formatTimestamp(iso, 'date') : null
}

export const SPACES_KEY = ['spaces'] as const
export const CURRENT_SPACE_KEY = ['spaces', 'current'] as const
/** Under `SPACES_KEY` so one invalidation reaches both on accept. */
export const INVITATIONS_KEY = ['spaces', 'invitations'] as const

export function membersKey(id: string) {
  return ['spaces', id, 'members'] as const
}

const households = rpcClient(HouseholdService)

export function useSpaces() {
  return useQuery({
    queryKey: SPACES_KEY,
    queryFn: ({ signal }) =>
      unary(HouseholdService.method.listSpaces, async () => {
        const listed = await households.listSpaces({}, { signal })
        return fromWire<{ spaces: Space[] }>(ListSpacesResponseSchema, listed).spaces
      }),
  })
}

/** Read from the server, never guessed, so a write cannot land in the wrong space. */
export function useCurrentSpace() {
  return useQuery({
    queryKey: CURRENT_SPACE_KEY,
    queryFn: ({ signal }) =>
      unary(HouseholdService.method.getCurrentSpace, async () => {
        const current = await households.getCurrentSpace({}, { signal })
        return fromWire<{ space: Space }>(GetCurrentSpaceResponseSchema, current).space
      }),
    retry: false,
  })
}

/** Whether the person owns (or administers) the current space; false while it loads. */
export function useOwnsSpace(): boolean {
  return useCurrentSpace().data?.is_owner === true
}

export function useInvitations() {
  return useQuery({
    queryKey: INVITATIONS_KEY,
    queryFn: ({ signal }) =>
      unary(HouseholdService.method.listInvitations, async () => {
        const listed = await households.listInvitations({}, { signal })
        return fromWire<{ invitations: Invitation[] }>(ListInvitationsResponseSchema, listed).invitations
      }),
  })
}

export function useMembers(spaceId: string | null) {
  return useQuery({
    queryKey: membersKey(spaceId ?? 'none'),
    queryFn: ({ signal }) =>
      unary(HouseholdService.method.listMembers, async () => {
        const listed = await households.listMembers({ spaceId: spaceId ?? '' }, { signal })
        return fromWire<{ members: Member[] }>(ListMembersResponseSchema, listed).members
      }),
    enabled: spaceId !== null,
  })
}

/**
 * Every write here can change the listing, the members and the current space,
 * so the shared `SPACES_KEY` prefix is invalidated.
 */
const SPACE_WRITE: Invalidates = [SPACES_KEY]

export function useCreateSpace() {
  return useInvalidatingMutation(
    (name: string) =>
      unary(HouseholdService.method.createSpace, async () => {
        const created = await households.createSpace({ name })
        return fromWire<{ space: Space }>(CreateSpaceResponseSchema, created).space
      }),
    SPACE_WRITE,
    { failure: 'That space was not created' },
  )
}

export function useRenameSpace() {
  return useInvalidatingMutation(
    ({ id, name }: { id: string; name: string }) =>
      unary(HouseholdService.method.renameSpace, async () => {
        const renamed = await households.renameSpace(toPatch(RenameSpaceRequestSchema, { space_id: id }, { name }))
        return fromWire<{ space: Space }>(RenameSpaceResponseSchema, renamed).space
      }),
    SPACE_WRITE,
  )
}

/** The backup set the server took first, or null where it writes none. */
export interface SpaceDeleted {
  backup_set: string | null
}

/**
 * The space's owner only, never an admin, with the name typed out; the server
 * refuses a caller's last space. The dialog shows the failure itself.
 */
export function useDeleteSpace() {
  return useInvalidatingMutation(
    ({ id, confirmName }: { id: string; confirmName: string }) =>
      unary(HouseholdService.method.deleteSpace, async () => {
        const deleted = await households.deleteSpace(
          toWire(DeleteSpaceRequestSchema, { space_id: id, confirm_name: confirmName }),
        )
        return fromWire<SpaceDeleted>(DeleteSpaceResponseSchema, deleted)
      }),
    SPACE_WRITE,
    { failure: false },
  )
}

/** The rule the server deletes by: the name exactly, spaces at either end forgiven. */
export function confirmsSpaceName(typed: string, name: string): boolean {
  return typed.trim() === name
}

/** Only an owner deletes, and only a space that is not the caller's last. */
export function canDeleteSpace(space: Space, spaceCount: number): boolean {
  return space.role === 'owner' && spaceCount > 1
}

/**
 * Any member may set these (renaming is an owner's act). `null` clears a
 * preference back to its default; an omitted key is left alone.
 */
export function useSavePreferences() {
  return useInvalidatingMutation(
    (preferences: {
      default_date_range?: string | null
      sidebar_account_types?: string[] | null
      /** Changing it remakes every stored conversion server side. */
      primary_currency?: string
    }) =>
      unary(HouseholdService.method.updatePreferences, async () => {
        const updated = await households.updatePreferences(toPatch(UpdatePreferencesRequestSchema, {}, preferences))
        return fromWire<{ space: Space }>(UpdatePreferencesResponseSchema, updated).space
      }),
    SPACE_WRITE,
    { failure: 'Those preferences were not saved' },
  )
}

/** The caller's own arrangement of the current space's dashboard; null when there is none. */
export function getDashboardLayout(signal?: AbortSignal) {
  return unary(HouseholdService.method.getDashboardLayout, async () => {
    const stored = await households.getDashboardLayout({}, { signal })
    return fromWire<{ layout: unknown }>(GetDashboardLayoutResponseSchema, stored)
  })
}

/** Null clears it back to the built-in order. */
export function saveDashboardLayout(layout: unknown) {
  return unary(HouseholdService.method.saveDashboardLayout, async () => {
    await households.saveDashboardLayout(toWire(SaveDashboardLayoutRequestSchema, { layout }))
  })
}

export function useInviteMember() {
  return useInvalidatingMutation(
    ({ spaceId, email, role }: { spaceId: string; email: string; role: Role }) =>
      unary(HouseholdService.method.inviteMember, async () => {
        const invited = await households.inviteMember(
          toWire(InviteMemberRequestSchema, { space_id: spaceId, email, role }),
        )
        return fromWire<{ member: Member }>(InviteMemberResponseSchema, invited).member
      }),
    SPACE_WRITE,
  )
}

export function useChangeMemberRole() {
  return useInvalidatingMutation(
    ({ spaceId, membershipId, role }: { spaceId: string; membershipId: string; role: Role }) =>
      unary(HouseholdService.method.updateMember, async () => {
        const updated = await households.updateMember(
          toWire(UpdateMemberRequestSchema, { space_id: spaceId, membership_id: membershipId, role }),
        )
        return fromWire<{ member: Member }>(UpdateMemberResponseSchema, updated).member
      }),
    SPACE_WRITE,
  )
}

export function useAcceptInvitation() {
  return useInvalidatingMutation(
    (id: string) =>
      unary(HouseholdService.method.acceptInvitation, async () => {
        const accepted = await households.acceptInvitation({ membershipId: id })
        return fromWire<{ space: Space }>(AcceptInvitationResponseSchema, accepted).space
      }),
    SPACE_WRITE,
    { failure: false },
  )
}

export function useDeclineInvitation() {
  return useInvalidatingMutation(
    (id: string) =>
      unary(HouseholdService.method.declineInvitation, async () => {
        await households.declineInvitation({ membershipId: id })
      }),
    SPACE_WRITE,
    { failure: false },
  )
}

/** Also how the caller leaves a space. */
export function useRemoveMember() {
  return useInvalidatingMutation(
    ({ spaceId, membershipId }: { spaceId: string; membershipId: string }) =>
      unary(HouseholdService.method.removeMember, async () => {
        await households.removeMember({ spaceId, membershipId })
      }),
    SPACE_WRITE,
  )
}
