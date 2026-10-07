import { useState, type FormEvent } from 'react'

import { Button, Field, Input } from '@/components/ui'
import { useAuth } from '@/contexts/auth'
import { ApiError } from '@/lib/api'
import { MIN_PASSWORD_LENGTH, passwordLongEnough } from '@/lib/passwords'

export interface ChangePasswordFormProps {
  /** Label for the submit button, which differs between the two screens. */
  submitLabel?: string
  /** The sign-in-sized submit: true on the forced-change screen, false inside a settings card. */
  large?: boolean
  onDone?: () => void
}

/**
 * Replacing your own password, on the forced screen after a first sign-in and
 * in settings. The confirmation and length checks only save a round trip; the
 * server enforces both.
 */
export function ChangePasswordForm({
  submitLabel = 'Change password',
  large = false,
  onDone,
}: ChangePasswordFormProps) {
  const { user, changePassword } = useAuth()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [again, setAgain] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState(false)
  const [busy, setBusy] = useState(false)

  // A passkey or provider account has no current password to prove.
  const hasPassword = user?.has_password ?? true

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setError(null)
    setDone(false)

    if (next !== again) {
      setError('The two new passwords do not match.')
      return
    }
    if (!passwordLongEnough(next)) {
      setError(`Use at least ${MIN_PASSWORD_LENGTH} characters.`)
      return
    }

    setBusy(true)
    try {
      await changePassword(current, next)
      setCurrent('')
      setNext('')
      setAgain('')
      setDone(true)
      onDone?.()
    } catch (failure) {
      setError(
        failure instanceof ApiError
          ? (failure.detail ?? 'The password could not be changed.')
          : 'Could not reach the server.',
      )
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="auth__form" onSubmit={submit}>
      {hasPassword ? (
        <Field label="Current password">
          <Input
            type="password"
            value={current}
            onChange={(event) => setCurrent(event.target.value)}
            autoComplete="current-password"
            required
          />
        </Field>
      ) : null}

      <Field label="New password" hint={`At least ${MIN_PASSWORD_LENGTH} characters.`}>
        <Input
          type="password"
          value={next}
          onChange={(event) => setNext(event.target.value)}
          autoComplete="new-password"
          required
        />
      </Field>

      <Field label="Repeat new password">
        <Input
          type="password"
          value={again}
          onChange={(event) => setAgain(event.target.value)}
          autoComplete="new-password"
          required
        />
      </Field>

      {error ? (
        <p className="auth__error" role="alert">
          {error}
        </p>
      ) : null}
      {done ? (
        <p className="auth__notice" role="status">
          Password changed. Every other signed-in browser has been signed out.
        </p>
      ) : null}

      <Button type="submit" variant="primary" size={large ? 'lg' : 'md'} disabled={busy}>
        {busy ? 'Saving…' : submitLabel}
      </Button>
    </form>
  )
}
