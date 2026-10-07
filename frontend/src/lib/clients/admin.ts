/**
 * The server administrator's surface, all behind `/admin`, which the API
 * refuses to non-superusers. A minted password comes back exactly once; the
 * OIDC client secret never comes back.
 */

import { useMutation, useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { plural } from '@/lib/format'
import { useInvalidatingMutation, type Invalidates } from '@/lib/queryClient'
import type { Role } from '@/lib/clients/spaces'

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
  /** Per field: 'database' when saved here, 'environment' otherwise. */
  sources: Record<string, string>
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

export function useAdminUsers(enabled = true) {
  return useQuery({
    queryKey: ADMIN_USERS_KEY,
    queryFn: ({ signal }) => api.get<AdminUser[]>('/admin/users', undefined, signal),
    enabled,
  })
}

export function useAdminSpaces(enabled = true) {
  return useQuery({
    queryKey: ADMIN_SPACES_KEY,
    queryFn: ({ signal }) => api.get<AdminSpace[]>('/admin/spaces', undefined, signal),
    enabled,
  })
}

export function useOidcSettings(enabled = true) {
  return useQuery({
    queryKey: ADMIN_OIDC_KEY,
    queryFn: ({ signal }) => api.get<OidcSettings>('/admin/oidc', undefined, signal),
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
  /** Present only when the server minted the password; never readable again. */
  temporary_password?: string
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
    (form: NewUserForm) => api.post<CreatedUser>('/admin/users', newUserBody(form)),
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
  return useInvalidatingMutation(({ id, ...changes }: UserPatch) => {
    return api.patch<AdminUser>(`/admin/users/${id}`, changes)
  }, ADMIN_WRITE)
}

export interface PasswordReset {
  id: string
  password?: string
  must_change_password: boolean
}

export function useSetUserPassword() {
  return useInvalidatingMutation(({ id, password, must_change_password }: PasswordReset) => {
    return api.post<{ temporary_password?: string }>(`/admin/users/${id}/password`, {
      password: password ?? '',
      must_change_password,
    })
  }, ADMIN_WRITE)
}

export interface MembershipAdd {
  userId: string
  space_id: string
  role: Role
}

export function useAddMembership() {
  return useInvalidatingMutation(({ userId, space_id, role }: MembershipAdd) => {
    return api.post<AdminMembership>(`/admin/users/${userId}/memberships`, { space_id, role })
  }, ADMIN_WRITE)
}

export function useRemoveMembership() {
  return useInvalidatingMutation(({ userId, id }: { userId: string; id: string }) => {
    return api.delete<void>(`/admin/users/${userId}/memberships/${id}`)
  }, ADMIN_WRITE,
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
}

export function useSaveOidcSettings() {
  return useInvalidatingMutation(
    (form: OidcForm) =>
      api.put<OidcSettings>('/admin/oidc', {
        ...form,
        provider_name: form.provider_name.trim(),
        discovery_url: form.discovery_url.trim(),
        client_id: form.client_id.trim(),
        scopes: form.scopes.split(/\s+/).filter((scope) => scope !== ''),
      }),
    ADMIN_WRITE,
  )
}

export function useTestOidcSettings() {
  return useMutation({
    mutationFn: (discovery_url: string) =>
      api.post<OidcProbe>('/admin/oidc/test', { discovery_url: discovery_url.trim() }),
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

export interface BackupSet {
  name: string
  created_at: string
  trigger: string
  encrypted: boolean
  recipients: string[]
  /** pg_restore read the whole dump as it was written. */
  verified: boolean
  /** Every part is on disk at its recorded size. */
  intact: boolean
  problem: string | null
  bytes: number
  schema_version: number
  parts: BackupPart[]
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
  sources: Record<string, string>
  /** Whether the next set will be encrypted. */
  encrypted: boolean
  next_run: string | null
  running: boolean
  /** Why a run cannot start right now. */
  problem: string | null
  dump_version: string
  server_version: string
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
    queryFn: ({ signal }) => api.get<Backups>('/admin/backups', undefined, signal),
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
    (body: BackupSettingsBody) => api.put<Backups>('/admin/backups/settings', body),
    [ADMIN_BACKUPS_KEY],
    { failure: 'The backup settings were not saved' },
  )
}

export function useRunBackup() {
  return useInvalidatingMutation(
    () => api.post<Backups>('/admin/backups/run', {}),
    [ADMIN_BACKUPS_KEY],
    { failure: 'The backup did not start' },
  )
}

export interface RehearseInput {
  name: string
  identity: string
  /** An encrypted SSH private key's passphrase; empty for any other identity. */
  passphrase: string
}

/**
 * The rehearsal's request. The identity and passphrase ride in this one body
 * and nowhere else.
 */
export function rehearseRequest({ name, identity, passphrase }: RehearseInput) {
  return {
    path: `/admin/backups/sets/${encodeURIComponent(name)}/rehearse`,
    body: { identity, passphrase },
  }
}

export function useRehearseBackup() {
  return useMutation({
    mutationFn: (input: RehearseInput) => {
      const request = rehearseRequest(input)
      return api.post<BackupRehearsal>(request.path, request.body)
    },
    meta: { failure: 'The rehearsal did not finish' },
  })
}

/** The running build. `commit` is empty for a build that was not told one. */
export interface ServerInfo {
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

export function useServerInfo(enabled = true) {
  return useQuery({
    queryKey: ADMIN_SERVER_KEY,
    queryFn: ({ signal }) => api.get<ServerInfo>('/admin/server', undefined, signal),
    enabled,
  })
}

export function useServerSettings(enabled = true) {
  return useQuery({
    queryKey: ADMIN_SERVER_SETTINGS_KEY,
    queryFn: ({ signal }) =>
      api.get<{ settings: ServerSetting[] }>('/admin/server/settings', undefined, signal),
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
      api.put<{ settings: ServerSetting[] }>('/admin/server/settings', body),
    [ADMIN_SERVER_SETTINGS_KEY],
    { failure: 'The settings were not saved' },
  )
}
