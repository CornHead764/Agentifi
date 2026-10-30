import { useState, type FormEvent } from 'react'

import { Button, Field, Input } from '@/components/ui'
import { ApiError } from '@/lib/api'
import { createFirstAccount } from '@/lib/clients/security'
import { MIN_PASSWORD_LENGTH, passwordLongEnough } from '@/lib/passwords'

/**
 * The sign-up a server with no accounts offers, on the sign-in card. The
 * account it makes administers the server and owns the first space; once any
 * account exists the server refuses it, which `onClosed` hands back to the
 * sign-in form.
 */
export function FirstAccountForm({
  onCreated,
  onClosed,
}: {
  onCreated: (token: string) => Promise<void>
  onClosed: () => void
}) {
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [space, setSpace] = useState('Household')
  const [password, setPassword] = useState('')
  const [repeat, setRepeat] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const mismatch = repeat !== '' && repeat !== password
  const ready = email.trim() !== '' && passwordLongEnough(password) && repeat === password

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!ready) return
    setError(null)
    setBusy(true)
    try {
      const token = await createFirstAccount({
        email: email.trim(),
        full_name: name.trim(),
        password,
        space_name: space.trim(),
      })
      await onCreated(token)
    } catch (failure) {
      if (failure instanceof ApiError && failure.status === 409) {
        onClosed()
        return
      }
      setError(
        failure instanceof ApiError
          ? (failure.detail ?? 'The account was not created.')
          : 'Could not reach the server.',
      )
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <p className="auth__lede">
        This server has no accounts yet. The one you create here administers it.
      </p>
      <form className="auth__form" onSubmit={submit}>
        <Field label="Email">
          <Input
            type="email"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            autoFocus
            required
          />
        </Field>
        <Field label="Your name" hint="Optional.">
          <Input value={name} onChange={(event) => setName(event.target.value)} autoComplete="name" />
        </Field>
        <Field label="Password" hint={`At least ${MIN_PASSWORD_LENGTH} characters.`}>
          <Input
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            autoComplete="new-password"
            required
          />
        </Field>
        <Field label="Repeat the password" error={mismatch ? 'The passwords differ.' : undefined}>
          <Input
            type="password"
            value={repeat}
            onChange={(event) => setRepeat(event.target.value)}
            autoComplete="new-password"
            required
          />
        </Field>
        <Field label="Name your first space" hint="A set of accounts and budgets. Renamed any time.">
          <Input value={space} onChange={(event) => setSpace(event.target.value)} />
        </Field>

        <p className="auth__tip">
          <strong>Bringing history from Quicken Simplifi?</strong> Import it before you connect
          SimpleFIN. The import fills an empty space, and its accounts are then matched to your
          bank feeds, so nothing arrives twice.
        </p>

        {error ? (
          <p className="auth__error" role="alert">
            {error}
          </p>
        ) : null}

        <Button type="submit" variant="primary" size="lg" disabled={busy || !ready}>
          {busy ? 'Creating…' : 'Create account'}
        </Button>
      </form>
    </>
  )
}
