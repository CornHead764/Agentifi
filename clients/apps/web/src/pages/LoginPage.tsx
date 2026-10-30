import { useQueryClient } from '@tanstack/react-query'
import { Fingerprint } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { Navigate, useLocation } from 'react-router-dom'

import { BrandMark } from '@/components/BrandMark'
import { Button, Field, Input } from '@/components/ui'
import { useAuth } from '@/contexts/auth'
import { API_BASE, ApiError } from '@/lib/api'
import {
  authenticateWithPasskey,
  FIRST_ACCOUNT_KEY,
  useFirstAccountStatus,
  useOidcConfig,
} from '@/lib/clients/security'
import { isPasskeyPromptCancelled, passkeyMessageFor, signInFailureDetail } from '@/lib/passkeyErrors'
import { lastEmail } from '@/lib/session'
import { isSecureContext } from '@/lib/webauthn'

import { FirstAccountForm } from './FirstAccountForm'

/**
 * The way in: the password, then the second factor if the account has one,
 * as one card rather than two routes. A half-finished login holds a
 * short-lived handle that authorizes nothing on its own.
 *
 * No "create an account" and no "forgot password": accounts are made by whoever
 * runs the server, and a self-hosted install has no mail to send a reset
 * through. The one exception is a server with no accounts, which offers to
 * make the first.
 */
export function LoginPage() {
  const { status, signIn, completeSecondFactor, adoptToken } = useAuth()
  const location = useLocation()

  const [email, setEmail] = useState(lastEmail)
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [mfaToken, setMfaToken] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  // A refusal here means no provider button, which is also what `enabled:
  // false` means. Either way the password form is the whole screen.
  const { data: oidc } = useOidcConfig()
  const firstAccount = useFirstAccountStatus()
  const queryClient = useQueryClient()

  if (status === 'authenticated') {
    return <Navigate to={intendedDestination(location.state) ?? '/'} replace />
  }

  const submitPassword = async (event: FormEvent) => {
    event.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const result = await signIn(email.trim(), password)
      if (result.mfaToken) {
        setMfaToken(result.mfaToken)
        setPassword('')
      }
    } catch (failure) {
      setError(messageFor(failure))
    } finally {
      setBusy(false)
    }
  }

  const submitCode = async (event: FormEvent) => {
    event.preventDefault()
    if (!mfaToken) return
    setError(null)
    setBusy(true)
    try {
      await completeSecondFactor(mfaToken, code.trim())
    } catch (failure) {
      setError(messageFor(failure))
      setCode('')
    } finally {
      setBusy(false)
    }
  }

  const signInWithPasskey = async () => {
    setError(null)
    setPasskeyBusy(true)
    try {
      const token = await authenticateWithPasskey()
      await adoptToken(token)
    } catch (failure) {
      // A cancelled prompt is not a refusal: the person just closed it.
      if (!isPasskeyPromptCancelled(failure)) setError(passkeyMessageFor(failure))
    } finally {
      setPasskeyBusy(false)
    }
  }

  return (
    <div className="auth">
      <main className="auth__card">
        <div className="auth__brand">
          <span className="auth__mark" aria-hidden="true">
            <BrandMark className="auth__mark-glyph" />
          </span>
          <h1 className="auth__title">Agentifi</h1>
        </div>

        {firstAccount.data?.open ? (
          <FirstAccountForm
            onCreated={adoptToken}
            onClosed={() => {
              setError('This server already has an account. Sign in with it.')
              void queryClient.invalidateQueries({ queryKey: FIRST_ACCOUNT_KEY })
            }}
          />
        ) : mfaToken ? (
          <>
            <p className="auth__lede">
              Enter the six-digit code from your authenticator app. A recovery code works too.
            </p>
            <form className="auth__form" onSubmit={submitCode}>
              <Field label="Verification code">
                <Input
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  autoComplete="one-time-code"
                  inputMode="numeric"
                  autoFocus
                  required
                />
              </Field>

              {error ? (
                <p className="auth__error" role="alert">
                  {error}
                </p>
              ) : null}

              <Button type="submit" variant="primary" size="lg" disabled={busy}>
                {busy ? 'Checking…' : 'Verify'}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setMfaToken(null)
                  setCode('')
                  setError(null)
                }}
              >
                Start over
              </Button>
            </form>
          </>
        ) : (
          <>
            <p className="auth__lede">Sign in to Agentifi.</p>
            <form className="auth__form" onSubmit={submitPassword}>
              <Field label="Email or username">
                <Input
                  value={email}
                  onChange={(event) => setEmail(event.target.value)}
                  autoComplete="username"
                  autoCapitalize="none"
                  spellCheck={false}
                  autoFocus={!email}
                  required
                />
              </Field>

              <Field label="Password">
                <Input
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  autoComplete="current-password"
                  autoFocus={Boolean(email)}
                  required
                />
              </Field>

              {error ? (
                <p className="auth__error" role="alert">
                  {error}
                </p>
              ) : null}

              <Button type="submit" variant="primary" size="lg" disabled={busy}>
                {busy ? 'Signing in…' : 'Sign in'}
              </Button>

              {isSecureContext() || oidc?.enabled ? (
                <span className="auth__divider">or</span>
              ) : null}

              {/* WebAuthn refuses to run outside a secure context, so the
                  button is left off rather than failing. */}
              {isSecureContext() ? (
                <Button
                  type="button"
                  variant="secondary"
                  size="lg"
                  disabled={passkeyBusy}
                  onClick={() => void signInWithPasskey()}
                >
                  <Fingerprint size={16} aria-hidden="true" />
                  {passkeyBusy ? 'Waiting for your passkey…' : 'Sign in with a passkey'}
                </Button>
              ) : null}

              {oidc?.enabled ? (
                // A real navigation, not fetch: the endpoint answers with a
                // redirect to the provider, which the browser must follow.
                <Button variant="secondary" size="lg" asChild>
                  <a href={`${API_BASE}/auth/oidc/login`}>Continue with {oidc.provider_name}</a>
                </Button>
              ) : null}
            </form>
          </>
        )}

        {firstAccount.data?.open ? null : (
          <p className="auth__foot">
            Accounts are created by whoever runs this server, under Settings, Server admin, or
            with <code>agentifi user add</code>.
          </p>
        )}
      </main>
    </div>
  )
}

/**
 * Where the guard was sending them before it sent them here. Router state is
 * `unknown`: a reload, a bookmark or Back can produce any state.
 */
function intendedDestination(state: unknown): string | null {
  if (typeof state !== 'object' || state === null) return null
  const from: unknown = Reflect.get(state, 'from')
  // Same-document paths only. A state that arrived carrying an absolute URL
  // would turn this into an open redirect.
  return typeof from === 'string' && from.startsWith('/') && !from.startsWith('//') ? from : null
}

/**
 * What to show a person who could not get in. The server's 401 is one
 * sentence for a wrong password, an unknown address and a disabled account,
 * and is repeated unchanged so this does not answer what the server refused
 * to.
 */
function messageFor(failure: unknown): string {
  if (failure instanceof ApiError) {
    if (failure.status === 401)
      return 'That email address and password do not match an account.'
    return signInFailureDetail(failure)
  }
  return 'Could not reach the server.'
}
