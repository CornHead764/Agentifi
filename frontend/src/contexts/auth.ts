import { createContext, useContext } from 'react'

import { api } from '@/lib/api'
import { authKeys } from '@/lib/session'

export interface CurrentUser {
  id: string
  email: string
  full_name: string | null
  is_active: boolean
  is_superuser: boolean
  is_verified: boolean
  locale: string
  theme: string
  privacy_mode: boolean
  swipe_left_action: string
  swipe_right_action: string
  /** 0 is no animation at all. */
  animation_duration_ms?: number
  toast_duration_ms?: number
  last_login_at: string | null
  /**
   * True while the password was set by an operator. The API refuses everything
   * but `/auth/me`, `/auth/password` and `/auth/logout` until it is false.
   */
  must_change_password: boolean
  has_password: boolean
  has_totp: boolean
  has_oidc: boolean
}

export interface LoginResult {
  mfaToken: string | null
  methods: readonly string[]
}

export interface AuthValue {
  /** Null until `/auth/me` has answered; the app shows nothing over a guess. */
  user: CurrentUser | null
  /** `error` is a session that will never be confirmed (403, 500), kept apart from `loading` so it is not an endless spinner. */
  status: 'loading' | 'authenticated' | 'anonymous' | 'error'
  signIn: (email: string, password: string) => Promise<LoginResult>
  completeSecondFactor: (mfaToken: string, code: string) => Promise<void>
  /** Adopt a token minted elsewhere, such as the OIDC callback's fragment. */
  adoptToken: (token: string) => Promise<void>
  signOut: () => Promise<void>
  changePassword: (current: string, next: string) => Promise<void>
  /** Only meaningful while status is `error`. */
  error?: unknown
  retry?: () => void
}

export const AuthContext = createContext<AuthValue | null>(null)

export function useAuth(): AuthValue {
  const value = useContext(AuthContext)
  if (!value) throw new Error('useAuth must be used inside <AuthProvider>')
  return value
}

/**
 * A stored token is not yet a session: until `/auth/me` confirms it the status
 * is `loading`. A settled failure is `error`, since its retries are spent. A
 * 401 never arrives here: the API client drops the token, which is `anonymous`.
 */
export function sessionStatus(
  token: string | null,
  user: CurrentUser | null,
  failed: boolean,
): AuthValue['status'] {
  if (token === null) return 'anonymous'
  if (user) return 'authenticated'
  return failed ? 'error' : 'loading'
}

/**
 * No `retry` override: the client default skips a 401 but gives a 500 or a
 * dropped connection during app load its retries.
 */
export function meQueryOptions(token: string | null) {
  return {
    queryKey: authKeys.meFor(token),
    queryFn: ({ signal }: { signal: AbortSignal }) =>
      api.get<CurrentUser>('/auth/me', undefined, signal),
    enabled: token !== null,
    staleTime: 5 * 60 * 1000,
  }
}

export function userAfterPasswordChange(user: CurrentUser): CurrentUser {
  return { ...user, must_change_password: false, has_password: true }
}
