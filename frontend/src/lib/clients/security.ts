/**
 * Passkeys, the second factor and SSO, always read from the server. TOTP
 * changes move `has_totp` on `/auth/me`, so they invalidate that query too.
 */

import { useMutation, useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
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

/** Identity-free: no email goes in, so only a discoverable credential works. */
export async function authenticateWithPasskey(): Promise<string> {
  const { challenge_id, options } = await api.post<PasskeyOptionsResponse<PasskeyRequestOptionsJSON>>(
    '/auth/passkeys/authenticate/options',
  )
  const assertion = await navigator.credentials.get(requestOptionsFromServer(options))
  if (!(assertion instanceof PublicKeyCredential)) {
    throw new Error('The browser did not return a passkey.')
  }
  const body = await api.post<{ access_token: string; token_type: string }>(
    '/auth/passkeys/authenticate/verify',
    { challenge_id, credential: authenticationCredentialToJSON(assertion) },
  )
  return body.access_token
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
// `/auth/me`'s own key is keyed on the token, which this module does not
// hold; invalidating by prefix reaches whichever token is current.
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

/** The account administers the server; the token signs it straight in. */
export async function createFirstAccount(account: FirstAccount): Promise<string> {
  const created = await api.post<{ access_token: string }>('/auth/first-account', account)
  return created.access_token
}

// --- Display helpers -------------------------------------------------------

export function recoveryCodesLabel(remaining: number): string {
  return `${plural(remaining, 'recovery code')} left`
}

export function passkeyUsageLabel(key: Passkey): string {
  const added = `Added ${formatTimestamp(key.created_at, 'date')}`
  if (!key.last_used_at) return `${added} · never used`
  return `${added} · last used ${formatTimestamp(key.last_used_at, 'date')}`
}
