import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useReducer, useState, type ReactNode } from 'react'

import { adoptUser, onActiveSpaceChange, setActiveSpace } from '@/lib/activeSpace'
import { api } from '@/lib/api'
import {
  accessToken,
  clearAccessToken,
  onAccessTokenChange,
  rememberEmail,
  setAccessToken,
  authKeys,
} from '@/lib/session'

import {
  AuthContext,
  meQueryOptions,
  sessionStatus,
  userAfterPasswordChange,
  type AuthValue,
  type CurrentUser,
  type LoginResult,
} from './auth'
import { SpaceContext, type SpaceValue } from './space'

interface TokenBody {
  access_token: string
  token_type: string
  mfa_token?: string
  mfa_methods?: string[]
}

/**
 * Mirrors the token (source of truth in lib/session) into state. The token is
 * part of the account query's key, so a different sign-in is never served
 * from the previous person's cache.
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [token, setToken] = useState<string | null>(() => accessToken())

  // A change of token empties the cache. Here rather than in an effect, which
  // would also fire on the first render and drop queries already started.
  useEffect(
    () =>
      onAccessTokenChange((next) => {
        queryClient.clear()
        setToken(next)
      }),
    [queryClient],
  )

  const me = useQuery(meQueryOptions(token))

  const user = token === null ? null : (me.data ?? null)

  // Read during render, not in an effect: a child's query effects run before
  // its parent's and would fetch under the server's default space.
  const [, spaceSwitched] = useReducer((count: number) => count + 1, 0)
  const activeSpaceId = adoptUser(user?.id ?? null)

  useEffect(
    () =>
      onActiveSpaceChange(() => {
        // Every cached row belongs to its space. The account record is put
        // back, or the session would flip to `loading` and unmount the screen.
        const account = queryClient.getQueryData<CurrentUser>(authKeys.meFor(accessToken()))
        queryClient.clear()
        if (account) queryClient.setQueryData(authKeys.meFor(accessToken()), account)
        spaceSwitched()
      }),
    [queryClient],
  )

  const space: SpaceValue = useMemo(
    () => ({ activeSpaceId, setActiveSpace }),
    [activeSpaceId],
  )

  const retry = useCallback(() => {
    void queryClient.refetchQueries({ queryKey: authKeys.me })
  }, [queryClient])

  const signIn = useCallback(async (email: string, password: string): Promise<LoginResult> => {
    const body = await api.form<TokenBody>('/auth/token', {
      // `username` is what the OAuth2 password grant calls the identifier.
      username: email,
      password,
    })
    rememberEmail(email)

    if (body.access_token) {
      setAccessToken(body.access_token)
      return { mfaToken: null, methods: [] }
    }
    return { mfaToken: body.mfa_token ?? null, methods: body.mfa_methods ?? [] }
  }, [])

  const completeSecondFactor = useCallback(async (mfaToken: string, code: string) => {
    const body = await api.post<TokenBody>('/auth/token/mfa', { mfa_token: mfaToken, code })
    setAccessToken(body.access_token)
  }, [])

  const adoptToken = useCallback(async (next: string) => {
    setAccessToken(next)
  }, [])

  const signOut = useCallback(async () => {
    try {
      await api.post('/auth/logout')
    } catch {
      // The token is thrown away either way; a failed logout does not keep anyone signed in.
    }
    clearAccessToken()
  }, [])

  const changePassword = useCallback(
    async (current: string, next: string) => {
      // The change signs out every session including this one, so the
      // replacement token must be adopted.
      const body = await api.post<TokenBody>('/auth/password', {
        current_password: current,
        new_password: next,
      })
      // Adopting it clears the cache and would unmount this form; re-seeding
      // the account record under the new token keeps the session authenticated.
      const previous = queryClient.getQueryData<CurrentUser>(authKeys.meFor(accessToken()))
      setAccessToken(body.access_token)
      if (previous) {
        queryClient.setQueryData(
          ['auth', 'me', body.access_token],
          userAfterPasswordChange(previous),
        )
      }
    },
    [queryClient],
  )

  const value: AuthValue = useMemo(
    () => ({
      user,
      status: sessionStatus(token, user, me.status === 'error'),
      error: me.error,
      retry,
      signIn,
      completeSecondFactor,
      adoptToken,
      signOut,
      changePassword,
    }),
    [
      user,
      token,
      me.status,
      me.error,
      retry,
      signIn,
      completeSecondFactor,
      adoptToken,
      signOut,
      changePassword,
    ],
  )

  return (
    <AuthContext value={value}>
      <SpaceContext value={space}>{children}</SpaceContext>
    </AuthContext>
  )
}
