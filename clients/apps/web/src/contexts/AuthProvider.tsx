import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useReducer, useState, type ReactNode } from 'react'

import { adoptUser, onActiveSpaceChange, setActiveSpace } from '@/lib/activeSpace'
import { api } from '@/lib/api'
import {
  authKeys,
  currentSession,
  markSignedIn,
  markSignedOut,
  onSessionChange,
  rememberEmail,
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

/** A first factor answers with a challenge, or with the session cookie and no challenge. */
interface LoginBody {
  mfa_token?: string
  mfa_methods?: string[]
}

/**
 * Mirrors the signed-in marker (source of truth in lib/session) into state.
 * The marker is part of the account query's key, so a different sign-in is
 * never served from the previous person's cache.
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [session, setSession] = useState<string | null>(() => currentSession())

  // A change of session empties the cache. Here rather than in an effect, which
  // would also fire on the first render and drop queries already started.
  useEffect(
    () =>
      onSessionChange((next) => {
        queryClient.clear()
        setSession(next)
      }),
    [queryClient],
  )

  const me = useQuery(meQueryOptions(session))

  const user = session === null ? null : (me.data ?? null)

  // Read during render, not in an effect: a child's query effects run before
  // its parent's and would fetch under the server's default space.
  const [, spaceSwitched] = useReducer((count: number) => count + 1, 0)
  const activeSpaceId = adoptUser(user?.id ?? null)

  useEffect(
    () =>
      onActiveSpaceChange(() => {
        // Every cached row belongs to its space. The account record is put
        // back, or the session would flip to `loading` and unmount the screen.
        const account = queryClient.getQueryData<CurrentUser>(authKeys.meFor(currentSession()))
        queryClient.clear()
        if (account) queryClient.setQueryData(authKeys.meFor(currentSession()), account)
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
    const body = await api.form<LoginBody>('/auth/token', {
      // `username` is what the OAuth2 password grant calls the identifier.
      username: email,
      password,
    })
    rememberEmail(email)

    if (body.mfa_token) return { mfaToken: body.mfa_token, methods: body.mfa_methods ?? [] }
    markSignedIn()
    return { mfaToken: null, methods: [] }
  }, [])

  const completeSecondFactor = useCallback(async (mfaToken: string, code: string) => {
    await api.post<void>('/auth/token/mfa', { mfa_token: mfaToken, code })
    markSignedIn()
  }, [])

  const adoptSession = useCallback(async () => {
    markSignedIn()
  }, [])

  const signOut = useCallback(async () => {
    try {
      await api.post('/auth/logout')
    } catch {
      // Signed out here either way; a failed logout does not keep anyone signed in.
    }
    markSignedOut()
  }, [])

  const changePassword = useCallback(
    async (current: string, next: string) => {
      // The change signs out every other session; this one carries on.
      await api.post<void>('/auth/password', { current_password: current, new_password: next })
      const key = authKeys.meFor(currentSession())
      const previous = queryClient.getQueryData<CurrentUser>(key)
      if (previous) queryClient.setQueryData(key, userAfterPasswordChange(previous))
    },
    [queryClient],
  )

  const value: AuthValue = useMemo(
    () => ({
      user,
      status: sessionStatus(session, user, me.status === 'error'),
      error: me.error,
      retry,
      signIn,
      completeSecondFactor,
      adoptSession,
      signOut,
      changePassword,
    }),
    [
      user,
      session,
      me.status,
      me.error,
      retry,
      signIn,
      completeSecondFactor,
      adoptSession,
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
