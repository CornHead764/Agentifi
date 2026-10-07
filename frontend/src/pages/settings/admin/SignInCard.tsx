import { CircleAlert, CircleCheck, PlugZap } from 'lucide-react'
import { useState } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import { Button, Card, Checkbox, CopyableSecret, Field, Input, Switch, useToast } from '@/components/ui'
import {
  useOidcSettings,
  useSaveOidcSettings,
  useTestOidcSettings,
  type OidcForm,
  type OidcProbe,
  type OidcSettings,
} from '@/lib/clients/admin'

/**
 * Single sign-on, configured from here. The environment stands behind any
 * field never saved, and `sources` says which is which per field. A save takes
 * effect on the next sign-in, not the next restart.
 */
export function SignInCard() {
  const settings = useOidcSettings()

  return (
    <Card
      title="Single sign-on"
      subtitle="OpenID Connect, alongside passwords and passkeys."
    >
      <QueryBoundary query={settings} rows={4}>
        {(stored) => <SignInForm stored={stored} />}
      </QueryBoundary>
    </Card>
  )
}

function SignInForm({
  stored,
}: {
  stored: OidcSettings
}) {
  const { show } = useToast()
  const [form, setForm] = useState<OidcForm>({
    enabled: stored.enabled,
    provider_name: stored.provider_name,
    discovery_url: stored.discovery_url,
    client_id: stored.client_id,
    // Always blank: the stored secret never comes back, and blank saves as
    // "keep what is stored".
    client_secret: '',
    scopes: stored.scopes.join(' '),
    auto_register: stored.auto_register,
    require_verified_email: stored.require_verified_email,
    link_existing_email: stored.link_existing_email,
  })
  const [probe, setProbe] = useState<OidcProbe | null>(null)

  const save = useSaveOidcSettings()
  const test = useTestOidcSettings()

  const change = (patch: Partial<OidcForm>) => setForm({ ...form, ...patch })
  const fromEnvironment = (field: string) =>
    stored.sources[field] === 'environment' ? 'Coming from the environment.' : undefined
  const ready =
    !form.enabled || (form.discovery_url.trim() !== '' && form.client_id.trim() !== '')

  return (
    <div className="stack">
      <div className="setting-row">
        <div className="setting-row__text">
          <p className="setting-row__label">Single sign-on</p>
          <p className="hint">
            {stored.configured
              ? `The login screen shows a ${stored.provider_name || 'provider'} button.`
              : 'The login screen shows a password form only.'}
          </p>
        </div>
        <Switch
          checked={form.enabled}
          onCheckedChange={(next) => change({ enabled: next })}
          label="Enabled"
        />
      </div>

      <Field
        label="Callback URL"
        hint="Register this at the provider exactly as written, or sign-in fails there."
      >
        <CopyableSecret value={stored.callback_url} />
      </Field>

      <div className="settings__form">
        <Field label="Provider name" hint={fromEnvironment('provider_name') ?? 'What the button says.'}>
          <Input
            value={form.provider_name}
            placeholder="Authentik"
            onChange={(event) => change({ provider_name: event.target.value })}
          />
        </Field>

        <Field
          label="Discovery URL"
          hint={fromEnvironment('discovery_url') ?? 'The full .well-known/openid-configuration URL.'}
        >
          <Input
            value={form.discovery_url}
            placeholder="https://idp.example.com/.well-known/openid-configuration"
            autoComplete="off"
            onChange={(event) => change({ discovery_url: event.target.value })}
          />
        </Field>

        <Field label="Client ID" hint={fromEnvironment('client_id')}>
          <Input
            value={form.client_id}
            autoComplete="off"
            onChange={(event) => change({ client_id: event.target.value })}
          />
        </Field>

        <Field
          label="Client secret"
          hint={
            stored.has_client_secret
              ? 'A secret is stored and never shown here. Leave blank to keep it.'
              : 'None stored. Leave blank for a public client.'
          }
        >
          <Input
            type="password"
            value={form.client_secret}
            placeholder={stored.has_client_secret ? '••••••••' : 'None'}
            autoComplete="off"
            onChange={(event) => change({ client_secret: event.target.value })}
          />
        </Field>

        <Field
          label="Scopes"
          hint={fromEnvironment('scopes') ?? 'Space-separated. openid email profile covers most providers.'}
        >
          <Input
            value={form.scopes}
            placeholder="openid email profile"
            onChange={(event) => change({ scopes: event.target.value })}
          />
        </Field>

      </div>

      <Field label="Who may sign in" as="group">
        <Checkbox
          checked={form.auto_register}
          onCheckedChange={(next) => change({ auto_register: next === true })}
          label="Let an unknown person from the provider create an account here"
        />
        <Checkbox
          checked={form.require_verified_email}
          onCheckedChange={(next) => change({ require_verified_email: next === true })}
          label="Only accept an address the provider says it has verified"
        />
        <Checkbox
          checked={form.link_existing_email}
          onCheckedChange={(next) => change({ link_existing_email: next === true })}
          label="Let a matching verified address adopt an account that already exists here"
        />
      </Field>

      {probe ? (
        <p className="hint">
          {probe.valid ? (
            <CircleCheck size={14} aria-hidden="true" />
          ) : (
            <CircleAlert size={14} aria-hidden="true" />
          )}{' '}
          {probe.valid ? `The provider answered as ${probe.issuer}.` : probe.message}
        </p>
      ) : null}

      <div className="import__actions">
        <Button
          variant="primary"
          disabled={!ready || save.isPending}
          title={ready ? undefined : 'Turning it on needs a discovery URL and a client id'}
          onClick={() =>
            save.mutate(form, {
              onSuccess: () => {
                // Cleared, so the secret does not sit in a form somebody walks
                // away from — and so a second save does not resend it.
                setForm((previous) => ({ ...previous, client_secret: '' }))
                show({
                  title: 'Sign-in settings saved',
                  description: 'They take effect now, without a restart.',
                  tone: 'success',
                })
              },
            })
          }
        >
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
        <Button
          variant="ghost"
          disabled={test.isPending || form.discovery_url.trim() === ''}
          onClick={() => {
            setProbe(null)
            test.mutate(form.discovery_url, { onSuccess: setProbe })
          }}
        >
          <PlugZap size={13} aria-hidden="true" />
          {test.isPending ? 'Testing…' : 'Test connection'}
        </Button>
      </div>
    </div>
  )
}
