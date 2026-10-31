/**
 * The server administrator's surface, AdminService, which the API refuses to
 * non-superusers. A minted password comes back exactly once; the OIDC client
 * secret never comes back.
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import {
  AddMembershipRequestSchema,
  AddMembershipResponseSchema,
  AdminService,
  CheckForUpdateResponseSchema,
  CreateUserRequestSchema,
  CreateUserResponseSchema,
  GetBackupsResponseSchema,
  GetOIDCSettingsResponseSchema,
  GetServerInfoResponseSchema,
  GetServerSettingsResponseSchema,
  GetUpdateResponseSchema,
  ListAllSpacesResponseSchema,
  ListUsersResponseSchema,
  RehearseBackupRequestSchema,
  RehearseBackupResponseSchema,
  RunBackupResponseSchema,
  SaveBackupSettingsRequestSchema,
  SaveBackupSettingsResponseSchema,
  SaveOIDCSettingsRequestSchema,
  SaveOIDCSettingsResponseSchema,
  SaveServerSettingsRequestSchema,
  SaveServerSettingsResponseSchema,
  SetUserPasswordRequestSchema,
  SetUserPasswordResponseSchema,
  TestOIDCSettingsResponseSchema,
  UpdateUserRequestSchema,
  UpdateUserResponseSchema,
} from '@/gen/agentifi/v1/admin_pb'
import { plural } from '@/lib/format'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import type { Role } from '@/lib/clients/spaces'
import { fromWire, toPatch, toWire } from '@/lib/rpc/wire'
import { rpcClient, unary } from '@/lib/rpcSession'

export interface AdminMembership {
  id: string
  space_id: string
  space_name: string
  role: Role
  /** False for an invitation, which grants nothing. */
  accepted: boolean
}

export interface AdminUser {
  id: string
  email: string
  full_name: string | null
  is_active: boolean
  is_superuser: boolean
  is_verified: boolean
  must_change_password: boolean
  created_at: string
  last_login_at: string | null
  has_password: boolean
  has_totp: boolean
  has_oidc: boolean
  oidc_issuer: string
  passkey_count: number
  memberships: AdminMembership[]
}

export interface AdminSpaceMember {
  membership_id: string
  user_id: string
  email: string
  full_name: string | null
  role: Role
  accepted: boolean
}

export interface AdminSpace {
  id: string
  name: string
  primary_currency: string
  timezone: string
  created_at: string
  members: AdminSpaceMember[]
}

export interface OidcSettings {
  enabled: boolean
  provider_name: string
  discovery_url: string
  client_id: string
  has_client_secret: boolean
  scopes: string[]
  auto_register: boolean
  require_verified_email: boolean
  link_existing_email: boolean
  groups_claim: string
  /** Empty leaves administrator rights to this screen. */
  admin_groups: string[]
  /** Per field: 'database' when saved here, 'environment' otherwise. */
  sources: Record<string, SettingSource>
  /** What has to be registered at the provider. */
  callback_url: string
  configured: boolean
}

export interface OidcProbe {
  valid: boolean
  issuer: string
  message: string
  authorization_endpoint: string
  token_endpoint: string
  userinfo_endpoint: string
  jwks_uri: string
}

const ADMIN_KEY = ['admin'] as const
export const ADMIN_USERS_KEY = ['admin', 'users'] as const
export const ADMIN_SPACES_KEY = ['admin', 'spaces'] as const
export const ADMIN_OIDC_KEY = ['admin', 'oidc'] as const

const admin = rpcClient(AdminService)

export function useAdminUsers(enabled = true) {
  return useQuery({
    queryKey: ADMIN_USERS_KEY,
    queryFn: ({ signal }) =>
      unary(AdminService.method.listUsers, async () => {
        const listed = await admin.listUsers({}, { signal })
        return fromWire<{ users: AdminUser[] }>(ListUsersResponseSchema, listed).users
      }),
    enabled,
  })
}

export function useAdminSpaces(enabled = true) {
  return useQuery({
    queryKey: ADMIN_SPACES_KEY,
    queryFn: ({ signal }) =>
      unary(AdminService.method.listAllSpaces, async () => {
        const listed = await admin.listAllSpaces({}, { signal })
        return fromWire<{ spaces: AdminSpace[] }>(ListAllSpacesResponseSchema, listed).spaces
      }),
    enabled,
  })
}

export function useOidcSettings(enabled = true) {
  return useQuery({
    queryKey: ADMIN_OIDC_KEY,
    queryFn: ({ signal }) =>
      unary(AdminService.method.getOIDCSettings, async () => {
        const read = await admin.getOIDCSettings({}, { signal })
        return fromWire<{ settings: OidcSettings }>(GetOIDCSettingsResponseSchema, read).settings
      }),
    enabled,
  })
}

/** No placement-less option: an account with no membership signs in and sees nothing. */
export interface NewUserForm {
  email: string
  full_name: string
  /** Blank asks the server to mint one. */
  password: string
  must_change_password: boolean
  is_superuser: boolean
  placement: Placement
  space_name: string
  currency: string
  space_id: string
  role: Role
}

export interface NewUserBody {
  email: string
  full_name: string
  password?: string
  must_change_password: boolean
  is_superuser: boolean
  space_name?: string
  currency?: string
  space_id?: string
  role?: Role
}

export interface CreatedUser {
  user: AdminUser
  /** Only when the server minted the password; never readable again. */
  temporary_password: string | null
}

/**
 * The two placements send disjoint fields so the server never has to pick. A
 * blank password is omitted: absent means "mint one".
 */
export function newUserBody(form: NewUserForm): NewUserBody {
  const body: NewUserBody = {
    email: form.email.trim(),
    full_name: form.full_name.trim(),
    must_change_password: form.must_change_password,
    is_superuser: form.is_superuser,
  }
  if (form.password.trim() !== '') body.password = form.password
  if (form.placement === 'existing') {
    body.space_id = form.space_id
    body.role = form.role
  } else {
    body.space_name = form.space_name.trim()
    body.currency = form.currency.trim().toUpperCase()
  }
  return body
}

/** The whole prefix: user and space listings both change. */
const ADMIN_WRITE: Invalidates = [ADMIN_KEY]

export function useCreateUser() {
  return useInvalidatingMutation(
    (form: NewUserForm) =>
      unary(AdminService.method.createUser, async () => {
        const created = await admin.createUser(toWire(CreateUserRequestSchema, newUserBody(form)))
        return fromWire<CreatedUser>(CreateUserResponseSchema, created)
      }),
    ADMIN_WRITE,
  )
}

export interface UserPatch {
  id: string
  full_name?: string
  is_active?: boolean
  is_superuser?: boolean
}

export function useUpdateUser() {
  return useInvalidatingMutation(
    ({ id, ...changes }: UserPatch) =>
      unary(AdminService.method.updateUser, async () => {
        const updated = await admin.updateUser(toPatch(UpdateUserRequestSchema, { user_id: id }, changes))
        return fromWire<{ user: AdminUser }>(UpdateUserResponseSchema, updated).user
      }),
    ADMIN_WRITE,
  )
}

export interface PasswordReset {
  id: string
  password?: string
  must_change_password: boolean
}

export function useSetUserPassword() {
  return useInvalidatingMutation(
    ({ id, password, must_change_password }: PasswordReset) =>
      unary(AdminService.method.setUserPassword, async () => {
        const set = await admin.setUserPassword(
          toWire(SetUserPasswordRequestSchema, { user_id: id, password: password ?? '', must_change_password }),
        )
        return fromWire<{ temporary_password: string | null }>(SetUserPasswordResponseSchema, set)
      }),
    ADMIN_WRITE,
  )
}

export interface MembershipAdd {
  userId: string
  space_id: string
  role: Role
}

export function useAddMembership() {
  return useInvalidatingMutation(
    ({ userId, space_id, role }: MembershipAdd) =>
      unary(AdminService.method.addMembership, async () => {
        const added = await admin.addMembership(
          toWire(AddMembershipRequestSchema, { user_id: userId, space_id, role }),
        )
        return fromWire<{ membership: AdminMembership }>(AddMembershipResponseSchema, added).membership
      }),
    ADMIN_WRITE,
  )
}

export function useRemoveMembership() {
  return useInvalidatingMutation(
    ({ userId, id }: { userId: string; id: string }) =>
      unary(AdminService.method.removeMembership, async () => {
        await admin.removeMembership({ userId, membershipId: id })
      }),
    ADMIN_WRITE,
    { failure: 'They were not removed from that space' },
  )
}

export interface OidcForm {
  enabled: boolean
  provider_name: string
  discovery_url: string
  client_id: string
  /** Blank keeps whatever is stored. */
  client_secret: string
  scopes: string
  auto_register: boolean
  require_verified_email: boolean
  link_existing_email: boolean
  groups_claim: string
  /** Comma-separated. */
  admin_groups: string
}

export function useSaveOidcSettings() {
  return useInvalidatingMutation(
    (form: OidcForm) =>
      unary(AdminService.method.saveOIDCSettings, async () => {
        const saved = await admin.saveOIDCSettings(
          toWire(SaveOIDCSettingsRequestSchema, {
            ...form,
            provider_name: form.provider_name.trim(),
            discovery_url: form.discovery_url.trim(),
            client_id: form.client_id.trim(),
            scopes: form.scopes.split(/\s+/).filter((scope) => scope !== ''),
            groups_claim: form.groups_claim.trim(),
            admin_groups: form.admin_groups
              .split(',')
              .map((group) => group.trim())
              .filter((group) => group !== ''),
          }),
        )
        return fromWire<{ settings: OidcSettings }>(SaveOIDCSettingsResponseSchema, saved).settings
      }),
    ADMIN_WRITE,
  )
}

export function useTestOidcSettings() {
  return useMutation({
    mutationFn: (discovery_url: string) =>
      unary(AdminService.method.testOIDCSettings, async () => {
        const probed = await admin.testOIDCSettings({ discoveryUrl: discovery_url.trim() })
        return fromWire<OidcProbe>(TestOIDCSettingsResponseSchema, probed)
      }),
  })
}

export type Placement = 'new' | 'existing'

export function asPlacement(value: string): Placement {
  return value === 'existing' ? 'existing' : 'new'
}

export function signInMethods(user: AdminUser): string {
  const methods: string[] = []
  if (user.has_password) methods.push('Password')
  if (user.passkey_count > 0) {
    methods.push(plural(user.passkey_count, 'passkey'))
  }
  if (user.has_totp) methods.push('Two-factor')
  if (user.has_oidc) methods.push('Single sign-on')
  return methods.length === 0 ? 'No way in yet' : methods.join(' · ')
}

/**
 * The server refuses the change anyway; this lets the button say so first. An
 * inactive administrator does not count.
 */
export function lastAdministrator(users: AdminUser[], user: AdminUser): boolean {
  if (!user.is_superuser || !user.is_active) return false
  return users.filter((one) => one.is_superuser && one.is_active).length <= 1
}

export interface BackupRecipient {
  /** `age1…`, or an SSH key's type and key without its comment. */
  recipient: string
  label: string
  added_at: string
  /** 'age', 'ssh-ed25519' or 'ssh-rsa'; empty for a key the server cannot read. */
  kind: string
  /** An SSH key's `SHA256:…`, as `ssh-keygen -l` prints it; empty for an age key. */
  fingerprint: string
}

export interface BackupRun {
  trigger: string
  status: 'running' | 'succeeded' | 'failed' | 'skipped'
  started_at: string
  finished_at: string | null
  set_name: string | null
  encrypted: boolean
  bytes: number
  error: string | null
}

export interface BackupPart {
  part: 'database' | 'attachments' | 'secrets'
  bytes: number
  count: number
}

/** One household a set holds, which can be restored on its own. */
export interface BackupHousehold {
  id: string
  /** The space's name on this install; null when it has none. */
  name: string | null
  bytes: number
  /** The set that took the snapshot: this one, or an earlier one it had not changed since. */
  taken_in: string
}

export interface BackupSet {
  name: string
  created_at: string
  trigger: string
  encrypted: boolean
  recipients: string[]
  /** Every SQLite snapshot passed integrity_check. */
  verified: boolean
  /** Every part is on disk at its recorded size. */
  intact: boolean
  problem: string | null
  bytes: number
  schema_version: number
  parts: BackupPart[]
  /** Empty for a set from before households were recorded one by one. */
  households: BackupHousehold[]
  /** Whether its stored connections open under this install's key; null when unrecorded. */
  key_matches: boolean | null
}

export interface Backups {
  /** False with BACKUP_DIR unset: nothing is written. */
  enabled: boolean
  /** BACKUP_DIR inside the container, a bind mount from the compose file. */
  directory: string
  at: string
  timezone: string
  keep_days: number
  recipients: BackupRecipient[]
  /** Per setting: 'database' when saved here, 'environment' otherwise. */
  sources: Record<string, SettingSource>
  /** Whether the next set will be encrypted. */
  encrypted: boolean
  next_run: string | null
  running: boolean
  /** Why a run cannot start right now. */
  problem: string | null
  runs: BackupRun[]
  sets: BackupSet[]
}

export interface BackupSettingsBody {
  at: string
  keep_days: number
  recipients: { recipient: string; label: string }[]
}

export interface BackupRehearsal {
  set: string
  /** The household rehearsed, or empty for the whole set. */
  household: string
  tables: { table: string; rows: number }[]
  rows: number
  attachments: number
  secrets: string[]
  warnings: string[]
}

export const ADMIN_BACKUPS_KEY = ['admin', 'backups'] as const

export function useBackups(enabled = true) {
  return useQuery({
    queryKey: ADMIN_BACKUPS_KEY,
    queryFn: ({ signal }) =>
      unary(AdminService.method.getBackups, async () => {
        const read = await admin.getBackups({}, { signal })
        return fromWire<{ backups: Backups }>(GetBackupsResponseSchema, read).backups
      }),
    enabled,
    refetchInterval: (query) => (query.state.data?.running ? 3000 : false),
  })
}

/**
 * The whole settings form as the server takes it. Only public keys travel:
 * an identity made on this screen stays in the browser.
 */
export function backupSettingsBody(
  backups: Pick<Backups, 'at' | 'keep_days' | 'recipients'>,
  changes: Partial<BackupSettingsBody> = {},
): BackupSettingsBody {
  return {
    at: changes.at ?? backups.at,
    keep_days: changes.keep_days ?? backups.keep_days,
    recipients:
      changes.recipients ??
      backups.recipients.map(({ recipient, label }) => ({ recipient, label })),
  }
}

/** The settings with one more public key. */
export function withRecipient(backups: Backups, recipient: string, label: string): BackupSettingsBody {
  const kept = backupSettingsBody(backups).recipients.filter((one) => one.recipient !== recipient)
  return backupSettingsBody(backups, {
    recipients: [...kept, { recipient: recipient.trim(), label: label.trim() }],
  })
}

/** The settings with one public key taken off. */
export function withoutRecipient(backups: Backups, recipient: string): BackupSettingsBody {
  return backupSettingsBody(backups, {
    recipients: backupSettingsBody(backups).recipients.filter((one) => one.recipient !== recipient),
  })
}

export function useSaveBackupSettings() {
  return useInvalidatingMutation(
    (body: BackupSettingsBody) =>
      unary(AdminService.method.saveBackupSettings, async () => {
        const saved = await admin.saveBackupSettings(toWire(SaveBackupSettingsRequestSchema, body))
        return fromWire<{ backups: Backups }>(SaveBackupSettingsResponseSchema, saved).backups
      }),
    [ADMIN_BACKUPS_KEY],
    { failure: 'The backup settings were not saved' },
  )
}

export function useRunBackup() {
  return useInvalidatingMutation(
    () =>
      unary(AdminService.method.runBackup, async () => {
        const started = await admin.runBackup({})
        return fromWire<{ backups: Backups }>(RunBackupResponseSchema, started).backups
      }),
    [ADMIN_BACKUPS_KEY],
    { failure: 'The backup did not start' },
  )
}

export interface RehearseInput {
  name: string
  identity: string
  /** An encrypted SSH private key's passphrase; empty for any other identity. */
  passphrase: string
  /** One household's id to rehearse alone; empty for the whole set. */
  household: string
}

/**
 * The rehearsal's request. The identity and passphrase ride in this one
 * request and nowhere else.
 */
export function rehearseRequest({ name, identity, passphrase, household }: RehearseInput) {
  return toWire(RehearseBackupRequestSchema, { name, identity, passphrase, household })
}

export function useRehearseBackup() {
  return useMutation({
    mutationFn: (input: RehearseInput) =>
      unary(AdminService.method.rehearseBackup, async () => {
        const rehearsed = await admin.rehearseBackup(rehearseRequest(input))
        return fromWire<BackupRehearsal>(RehearseBackupResponseSchema, rehearsed)
      }),
    meta: { failure: 'The rehearsal did not finish' },
  })
}

/** The running build. `version` and `commit` are empty for a build that was not told them. */
export interface ServerInfo {
  version: string
  commit: string
  built_at: string
  /** A local build from a working tree with changes. */
  modified: boolean
  go_version: string
  time_zone: string
  schema_version: number
}

export type SettingSource = 'environment' | 'database' | 'default'

export type SettingKind = 'toggle' | 'number' | 'time' | 'text' | 'list'

/** One environment setting as Server admin → Settings may save it. */
export interface ServerSetting {
  /** The environment variable. */
  key: string
  group: string
  label: string
  help: string
  kind: SettingKind
  /** What applies, written as the environment variable would be. */
  value: string
  default: string
  source: SettingSource
  /** Applies on save; otherwise when the server next starts. */
  live: boolean
  /** Saved, but the running server started with something else. */
  pending_restart: boolean
}

/** Per key: a typed value, or `null` for back to the default. */
export type SettingsDraft = Record<string, string | null>

export const ADMIN_SERVER_KEY = ['admin', 'server'] as const
export const ADMIN_SERVER_SETTINGS_KEY = ['admin', 'server', 'settings'] as const
export const ADMIN_UPDATE_KEY = ['admin', 'server', 'update'] as const

/** Whether a newer release than the running one is out, as the server last asked GitHub. */
export interface UpdateStatus {
  /** Off when UPDATE_CHECK_ENABLED is; nothing was asked. */
  enabled: boolean
  /** Empty for a build that was not told its version, which is never offered an update. */
  current_version: string
  /** The newest release tag, `v0.3.0`; empty until a check succeeds. */
  latest_version: string
  update_available: boolean
  /** The release's page. */
  url: string
  checked_at: string | null
  /** Why the last check failed; the last good answer still stands. */
  error: string
}

export function useServerInfo(enabled = true) {
  return useQuery({
    queryKey: ADMIN_SERVER_KEY,
    queryFn: ({ signal }) =>
      unary(AdminService.method.getServerInfo, async () => {
        const info = await admin.getServerInfo({}, { signal })
        return fromWire<ServerInfo>(GetServerInfoResponseSchema, info)
      }),
    enabled,
  })
}

/** The server keeps its answer for hours, so this rarely reaches GitHub. */
export function useUpdateStatus(enabled = true) {
  return useQuery({
    queryKey: ADMIN_UPDATE_KEY,
    queryFn: ({ signal }) =>
      unary(AdminService.method.getUpdate, async () => {
        const read = await admin.getUpdate({}, { signal })
        return fromWire<{ status: UpdateStatus }>(GetUpdateResponseSchema, read).status
      }),
    enabled,
    staleTime: 60 * 60 * 1000,
  })
}

/** Asks GitHub now rather than reading the server's kept answer. */
export function useCheckForUpdate() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () =>
      unary(AdminService.method.checkForUpdate, async () => {
        const checked = await admin.checkForUpdate({})
        return fromWire<{ status: UpdateStatus }>(CheckForUpdateResponseSchema, checked).status
      }),
    onSuccess: (status) => queryClient.setQueryData(ADMIN_UPDATE_KEY, status),
    meta: { failure: 'Could not check for an update' },
  })
}

export function useServerSettings(enabled = true) {
  return useQuery({
    queryKey: ADMIN_SERVER_SETTINGS_KEY,
    queryFn: ({ signal }) =>
      unary(AdminService.method.getServerSettings, async () => {
        const read = await admin.getServerSettings({}, { signal })
        return fromWire<{ settings: ServerSetting[] }>(GetServerSettingsResponseSchema, read)
      }),
    enabled,
  })
}

/** What a setting shows: the draft's value, the default it is going back to, or what applies. */
export function draftValue(setting: ServerSetting, draft: SettingsDraft): string {
  const edited = draft[setting.key]
  if (edited === null) return setting.default
  return edited ?? setting.value
}

/**
 * The whole form as the server takes it: every value saved here, with the
 * draft's changes. A setting left out goes back to its default, and one the
 * environment sets is never sent.
 */
export function serverSettingsBody(
  settings: ServerSetting[],
  draft: SettingsDraft,
): { values: Record<string, string> } {
  const values: Record<string, string> = {}
  for (const setting of settings) {
    if (setting.source === 'environment') continue
    const edited = draft[setting.key]
    if (edited === null) continue
    if (edited !== undefined) values[setting.key] = edited.trim()
    else if (setting.source === 'database') values[setting.key] = setting.value
  }
  return { values }
}

export function useSaveServerSettings() {
  return useInvalidatingMutation(
    (body: { values: Record<string, string> }) =>
      unary(AdminService.method.saveServerSettings, async () => {
        const saved = await admin.saveServerSettings(toWire(SaveServerSettingsRequestSchema, body))
        return fromWire<{ settings: ServerSetting[] }>(SaveServerSettingsResponseSchema, saved)
      }),
    [ADMIN_SERVER_SETTINGS_KEY, ADMIN_UPDATE_KEY],
    { failure: 'The settings were not saved' },
  )
}
