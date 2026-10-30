/**
 * Passkeys, the second factor, SSO and the signed-in sessions, always read
 * from the server. TOTP changes move `has_totp` on `/auth/me`, so they
 * invalidate that query too.
 */

import { useMutation, useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { markSignedOut } from '@/lib/session'
import {
  authenticationCredentialToJSON,
  creationOptionsFromServer,
  registrationCredentialToJSON,
  requestOptionsFromServer,
  type PasskeyCreationOptionsJSON,
  type PasskeyRequestOptionsJSON,
} from '@/lib/webauthn'
import { formatTimestamp, plural } from '@/lib/format'

export interface Passkey {
  id: string
  name: string
  created_at: string
  last_used_at: string | null
  transports: string[]
  rp_id: string | null
}

interface PasskeyOptionsResponse<T> {
  challenge_id: string
  options: T
}

export const PASSKEYS_KEY = ['security', 'passkeys'] as const

export function usePasskeys() {
  return useQuery({
    queryKey: PASSKEYS_KEY,
    queryFn: ({ signal }) => api.get<Passkey[]>('/auth/passkeys', undefined, signal),
  })
}

/** A blank `name` becomes "Passkey" on the server. */
async function registerPasskey(name: string): Promise<Passkey> {
  const { challenge_id, options } = await api.post<PasskeyOptionsResponse<PasskeyCreationOptionsJSON>>(
    '/auth/passkeys/register/options',
    name === '' ? undefined : { name },
  )
  const created = await navigator.credentials.create(creationOptionsFromServer(options))
  if (!(created instanceof PublicKeyCredential)) {
    throw new Error('The browser did not return a passkey.')
  }
  return api.post<Passkey>('/auth/passkeys/register/verify', {
    challenge_id,
    credential: registrationCredentialToJSON(created),
    name,
  })
}

export function usePasskeyRegistration() {
  return useInvalidatingMutation(
    (name: string) => registerPasskey(name),
    [PASSKEYS_KEY],
    { failure: 'Could not add that passkey' },
  )
}

export function useDeletePasskey() {
  return useInvalidatingMutation(
    (id: string) => api.delete<void>(`/auth/passkeys/${id}`),
    [PASSKEYS_KEY],
    { failure: 'Could not remove that passkey' },
  )
}

/** Identity-free: no email goes in, so only a discoverable credential works. The session arrives as a cookie. */
export async function authenticateWithPasskey(): Promise<void> {
  const { challenge_id, options } = await api.post<PasskeyOptionsResponse<PasskeyRequestOptionsJSON>>(
    '/auth/passkeys/authenticate/options',
  )
  const assertion = await navigator.credentials.get(requestOptionsFromServer(options))
  if (!(assertion instanceof PublicKeyCredential)) {
    throw new Error('The browser did not return a passkey.')
  }
  await api.post<void>('/auth/passkeys/authenticate/verify', {
    challenge_id,
    credential: authenticationCredentialToJSON(assertion),
  })
}

// --- Two-factor ---------------------------------------------------------------

export interface TOTPStatus {
  enabled: boolean
  recovery_codes_remaining: number
}

export interface TOTPEnrolment {
  secret: string
  otpauth_uri: string
}

/** Sent back exactly once. */
export interface TOTPConfirmation {
  recovery_codes: string[]
}

export const TOTP_KEY = ['security', 'totp'] as const
// `/auth/me`'s own key is keyed on the sign-in, which this module does not
// hold; invalidating by prefix reaches whichever is current.
const ME_KEY = ['auth', 'me'] as const

export function useTOTPStatus() {
  return useQuery({
    queryKey: TOTP_KEY,
    queryFn: ({ signal }) => api.get<TOTPStatus>('/auth/totp', undefined, signal),
  })
}

export function useEnrolTOTP() {
  return useMutation({
    meta: { failure: 'Could not start enrolment' },
    mutationFn: () => api.post<TOTPEnrolment>('/auth/totp/enrol'),
  })
}

export function useConfirmTOTP() {
  return useInvalidatingMutation(
    (code: string) => api.post<TOTPConfirmation>('/auth/totp/confirm', { code }),
    [TOTP_KEY, ME_KEY],
    { failure: 'That code was not accepted' },
  )
}

export function useDisableTOTP() {
  return useInvalidatingMutation(
    (code: string) => api.post<void>('/auth/totp/disable', { code }),
    [TOTP_KEY, ME_KEY],
    { failure: 'That code was not accepted' },
  )
}

// --- SSO -----------------------------------------------------------------------

export interface OidcConfig {
  enabled: boolean
  provider_name: string
}

export const OIDC_CONFIG_KEY = ['security', 'oidc-config'] as const

export function useOidcConfig() {
  return useQuery({
    queryKey: OIDC_CONFIG_KEY,
    queryFn: ({ signal }) => api.get<OidcConfig>('/auth/oidc/config', undefined, signal),
  })
}

// --- Sessions ------------------------------------------------------------------

export type SessionKind = 'web' | 'device' | 'personal'

export interface Session {
  id: string
  kind: SessionKind
  /** The browser and OS, or the name a personal token was given. */
  name: string
  created_at: string
  last_used_at: string
  expires_at: string
  /** The session this browser is using. */
  current: boolean
}

/** Sent back exactly once; only its digest is kept. */
export interface PersonalToken {
  token: string
  session: Session
}

export const SESSIONS_KEY = ['security', 'sessions'] as const

export function useSessions() {
  return useQuery({
    queryKey: SESSIONS_KEY,
    queryFn: ({ signal }) => api.get<Session[]>('/auth/sessions', undefined, signal),
  })
}

export function useRevokeSession() {
  return useInvalidatingMutation(
    (id: string) => api.delete<void>(`/auth/sessions/${id}`),
    [SESSIONS_KEY],
    { failure: 'Could not sign that session out' },
  )
}

export function useCreatePersonalToken() {
  return useInvalidatingMutation(
    (name: string) => api.post<PersonalToken>('/auth/personal-tokens', { name }),
    [SESSIONS_KEY],
    { failure: 'Could not create that token' },
  )
}

/** Ends every session, this one included, so this browser is signed out too. */
export function useSignOutEverywhere() {
  return useMutation({
    meta: { failure: 'Could not sign out everywhere' },
    mutationFn: async () => {
      await api.post<void>('/auth/sessions/revoke-all')
      markSignedOut()
    },
  })
}

// --- The first account -------------------------------------------------------

/** Open only while the server has no account at all. */
export interface FirstAccountStatus {
  open: boolean
}

export const FIRST_ACCOUNT_KEY = ['security', 'first-account'] as const

export function useFirstAccountStatus() {
  return useQuery({
    queryKey: FIRST_ACCOUNT_KEY,
    queryFn: ({ signal }) =>
      api.get<FirstAccountStatus>('/auth/first-account', undefined, signal),
  })
}

export interface FirstAccount {
  email: string
  full_name: string
  password: string
  space_name: string
}

/** The account administers the server; the session cookie signs it straight in. */
export async function createFirstAccount(account: FirstAccount): Promise<void> {
  await api.post<void>('/auth/first-account', account)
}

// --- Display helpers -------------------------------------------------------

export function recoveryCodesLabel(remaining: number): string {
  return `${plural(remaining, 'recovery code')} left`
}

const SESSION_KINDS: Record<SessionKind, string> = {
  web: 'Browser',
  device: 'App',
  personal: 'Personal token',
}

export function sessionUsageLabel(session: Session): string {
  return [
    SESSION_KINDS[session.kind],
    `last used ${formatTimestamp(session.last_used_at)}`,
    `signed in ${formatTimestamp(session.created_at, 'date')}`,
  ].join(' · ')
}

export function passkeyUsageLabel(key: Passkey): string {
  const added = `Added ${formatTimestamp(key.created_at, 'date')}`
  if (!key.last_used_at) return `${added} · never used`
  return `${added} · last used ${formatTimestamp(key.last_used_at, 'date')}`
}
