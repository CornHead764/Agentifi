import { KeyRound, Link2, Plus, ShieldCheck, ShieldOff } from 'lucide-react'
import { useState, type FormEvent } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import { ChangePasswordForm } from '@/components/ChangePasswordForm'
import { QrImage } from '@/components/QrImage'
import {
  Badge,
  Button,
  Callout,
  Card,
  ConfirmDialog,
  CopyableSecret,
  CopyButton,
  Dialog,
  DialogActions,
  DialogContent,
  EmptyState,
  Field,
  FormDialog,
  Input,
  List,
  ListRow,
  NameDialog,
  PageHeader,
  useConfirm,
  useHeld,
} from '@/components/ui'
import { useAuth } from '@/contexts/auth'
import {
  passkeyUsageLabel,
  recoveryCodesLabel,
  sessionUsageLabel,
  useConfirmTOTP,
  useCreatePersonalToken,
  useDeletePasskey,
  useDisableTOTP,
  useEnrolTOTP,
  useOidcConfig,
  usePasskeyRegistration,
  usePasskeys,
  useRevokeSession,
  useSessions,
  useSignOutEverywhere,
  useTOTPStatus,
  type Passkey,
  type PersonalToken,
  type Session,
  type TOTPEnrolment,
} from '@/lib/clients/security'
import { isSecureContext } from '@/lib/webauthn'
import { formatTimestamp } from '@agentifi/core/format'

export function SecuritySettings() {
  const { user, signOut } = useAuth()

  return (
    <>
      <PageHeader title="Security" />

      <Card
        title="Password"
        subtitle="Changing it signs out every other browser and app, and ends your personal tokens."
      >
        <div className="settings__form">
          <ChangePasswordForm />
        </div>
      </Card>

      <PasskeysCard />
      <TwoFactorCard />
      <SingleSignOnCard />
      <SessionsCard />

      <Card title="This session" subtitle={user?.email}>
        <div className="setting-row">
          <div className="setting-row__text">
            <p className="hint">
              {user?.last_login_at
                ? `Last signed in ${formatTimestamp(user.last_login_at)}.`
                : 'Signing out ends this session on this device only.'}
            </p>
          </div>
          <Button size="sm" onClick={() => void signOut()}>
            Sign out
          </Button>
        </div>
      </Card>
    </>
  )
}

/**
 * Enrolled passkeys. WebAuthn refuses to run outside a secure context, so plain
 * HTTP gets a sentence instead of a button that would fail silently.
 */
function PasskeysCard() {
  const passkeys = usePasskeys()
  const [adding, setAdding] = useState(false)

  const register = usePasskeyRegistration()
  const remove = useConfirm(useDeletePasskey(), {
    variables: (passkey: Passkey) => passkey.id,
  })

  const secure = isSecureContext()

  return (
    <Card
      title="Passkeys"
      subtitle="Sign in with a screen lock, security key or phone instead of a password."
      actions={
        secure ? (
          <Button variant="primary" size="sm" onClick={() => setAdding(true)}>
            <KeyRound size={13} aria-hidden="true" /> Add a passkey
          </Button>
        ) : null
      }
    >
      {!secure ? (
        <Callout tone="warning">
          Passkeys need HTTPS; this install is on plain HTTP. Everything else here still works.
        </Callout>
      ) : null}

      <QueryBoundary
        query={passkeys}
        rows={2}
        empty={(rows) => (rows.length === 0 ? <EmptyState compact title="No passkeys enrolled yet." /> : undefined)}
      >
        {(rows) => (
          <List>
            {rows.map((key) => (
              <ListRow
                key={key.id}
                title={key.name}
                sub={passkeyUsageLabel(key)}
                actions={
                  <Button size="sm" variant="danger" onClick={() => remove.ask(key)}>
                    Remove
                  </Button>
                }
              />
            ))}
          </List>
        )}
      </QueryBoundary>

      <AddPasskeyDialog
        open={adding}
        pending={register.isPending}
        onCancel={() => setAdding(false)}
        onConfirm={(name) => register.mutate(name, { onSuccess: () => setAdding(false) })}
      />

      <ConfirmDialog
        {...remove.dialog}
        title={remove.target ? `Remove ${remove.target.name}?` : 'Remove this passkey?'}
        confirmLabel="Remove passkey"
      >
        <p>Whatever device used this one will need another way to sign in.</p>
      </ConfirmDialog>
    </Card>
  )
}

interface AddPasskeyProps {
  pending: boolean
  onCancel: () => void
  onConfirm: (name: string) => void
}

function AddPasskeyDialog({ open, ...props }: AddPasskeyProps & { open: boolean }) {
  return (
    <FormDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) props.onCancel()
      }}
    >
      <AddPasskeyForm {...props} />
    </FormDialog>
  )
}

function AddPasskeyForm({ pending, onCancel, onConfirm }: AddPasskeyProps) {
  const [name, setName] = useState('')

  return (
    <DialogContent
      title="Add a passkey"
      description="Your browser will ask for your screen lock, a security key, or another device."
      footer={
        <DialogActions onCancel={onCancel}>
          <Button variant="primary" disabled={pending} onClick={() => onConfirm(name.trim())}>
            {pending ? 'Waiting for your device…' : 'Continue'}
          </Button>
        </DialogActions>
      }
    >
      <Field label="Name" hint="To tell it apart later.">
        <Input
          value={name}
          placeholder="YubiKey, iPhone, …"
          onChange={(event) => setName(event.target.value)}
        />
      </Field>
    </DialogContent>
  )
}

/**
 * The second factor's flow. The recovery codes are shown exactly once, after
 * confirmation, the only moment the server has the plaintext.
 */
type TotpFlow =
  | { step: 'status' }
  | { step: 'enrolling'; secret: string; otpauthUri: string }
  | { step: 'recovery-codes'; codes: string[] }

function TwoFactorCard() {
  const status = useTOTPStatus()
  const [flow, setFlow] = useState<TotpFlow>({ step: 'status' })
  const [code, setCode] = useState('')
  const [disabling, setDisabling] = useState(false)

  const enrol = useEnrolTOTP()
  const confirm = useConfirmTOTP()
  const disable = useDisableTOTP()

  const beginEnrolment = () => {
    enrol.mutate(undefined, {
      onSuccess: (data: TOTPEnrolment) => {
        setCode('')
        setFlow({ step: 'enrolling', secret: data.secret, otpauthUri: data.otpauth_uri })
      },
    })
  }

  const submitCode = (event: FormEvent) => {
    event.preventDefault()
    confirm.mutate(code.trim(), {
      onSuccess: (data) => {
        setCode('')
        setFlow({ step: 'recovery-codes', codes: data.recovery_codes })
      },
    })
  }

  return (
    <Card
      title="Two-factor authentication"
      subtitle="A code from an authenticator app, asked for alongside your password."
    >
      <QueryBoundary query={status} rows={1}>
        {(data) => {
          if (flow.step === 'enrolling') {
            return (
              <form className="auth__form" onSubmit={submitCode}>
                <p>
                  Scan with an authenticator app, or use the setup link or secret below.
                </p>
                <div className="totp-qr">
                  <QrImage
                    value={flow.otpauthUri}
                    label="Two-factor setup code, for an authenticator app"
                  />
                </div>
                <List>
                  <CopyRow label="Setup link" value={flow.otpauthUri} />
                  <CopyRow label="Secret" value={flow.secret} />
                </List>
                <Field label="Code from the app">
                  <Input
                    value={code}
                    onChange={(event) => setCode(event.target.value)}
                    autoComplete="one-time-code"
                    inputMode="numeric"
                    autoFocus
                    required
                  />
                </Field>
                <div className="setting-row__actions">
                  <Button type="submit" variant="primary" disabled={confirm.isPending}>
                    {confirm.isPending ? 'Confirming…' : 'Confirm'}
                  </Button>
                  <Button onClick={() => setFlow({ step: 'status' })}>Cancel</Button>
                </div>
              </form>
            )
          }

          if (flow.step === 'recovery-codes') {
            return <RecoveryCodes codes={flow.codes} onDone={() => setFlow({ step: 'status' })} />
          }

          return (
            <div className="setting-row">
              <div className="setting-row__text">
                <p className="setting-row__label">
                  {data.enabled ? (
                    <>
                      <ShieldCheck size={14} aria-hidden="true" /> On
                    </>
                  ) : (
                    <>
                      <ShieldOff size={14} aria-hidden="true" /> Off
                    </>
                  )}
                </p>
                <p className="hint">
                  {data.enabled
                    ? recoveryCodesLabel(data.recovery_codes_remaining)
                    : 'A password alone signs in while this is off.'}
                </p>
              </div>
              {data.enabled ? (
                <Button variant="danger" size="sm" onClick={() => setDisabling(true)}>
                  Turn off
                </Button>
              ) : (
                <Button
                  variant="primary"
                  size="sm"
                  disabled={enrol.isPending}
                  onClick={beginEnrolment}
                >
                  {enrol.isPending ? 'Starting…' : 'Turn on'}
                </Button>
              )}
            </div>
          )
        }}
      </QueryBoundary>

      <FormDialog open={disabling} onOpenChange={setDisabling}>
        <DisableTwoFactorForm
          pending={disable.isPending}
          onCancel={() => setDisabling(false)}
          onConfirm={(code) => disable.mutate(code, { onSuccess: () => setDisabling(false) })}
        />
      </FormDialog>
    </Card>
  )
}

function DisableTwoFactorForm({
  pending,
  onCancel,
  onConfirm,
}: {
  pending: boolean
  onCancel: () => void
  onConfirm: (code: string) => void
}) {
  const [code, setCode] = useState('')
  return (
    <DialogContent
      title="Turn off two-factor authentication?"
      description="Confirm with a current code or a recovery code."
      footer={
        <DialogActions onCancel={onCancel}>
          <Button
            variant="danger"
            disabled={pending || code.trim() === ''}
            onClick={() => onConfirm(code.trim())}
          >
            {pending ? 'Turning off…' : 'Turn off'}
          </Button>
        </DialogActions>
      }
    >
      <Field label="Code">
        <Input
          value={code}
          onChange={(event) => setCode(event.target.value)}
          autoComplete="one-time-code"
          inputMode="numeric"
          autoFocus
        />
      </Field>
    </DialogContent>
  )
}

function CopyRow({ label, value }: { label: string; value: string }) {
  return <ListRow title={label} sub={<code>{value}</code>} actions={<CopyButton size="sm" text={value} />} />
}

function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  return (
    <div className="auth__form">
      <p className="auth__notice" role="status">
        Save these somewhere safe — a password manager, or printed and put away. Each one signs
        you in once, in place of a code from your app, and this is the only time they are shown.
      </p>
      <List>
        {codes.map((code) => (
          <ListRow key={code} title={<code>{code}</code>} />
        ))}
      </List>
      <div className="setting-row__actions">
        <CopyButton text={codes.join('\n')} label="Copy all" copied="Recovery codes copied" />
        <Button type="button" variant="primary" onClick={onDone}>
          I&rsquo;ve saved these
        </Button>
      </div>
    </div>
  )
}

/**
 * Every live session and personal token. This browser's own row has no revoke
 * button, so it cannot be ended by a slip; signing out does that.
 */
function SessionsCard() {
  const sessions = useSessions()
  const [naming, setNaming] = useState(false)
  const [minted, setMinted] = useState<PersonalToken | null>(null)

  const create = useCreatePersonalToken()
  const revoke = useConfirm(useRevokeSession(), {
    variables: (session: Session) => session.id,
  })
  const everywhere = useConfirm<void>(useSignOutEverywhere())

  return (
    <Card
      title="Sessions"
      subtitle="Every browser, app and personal token signed in as you."
      actions={
        <Button size="sm" onClick={() => setNaming(true)}>
          <Plus size={13} aria-hidden="true" /> New personal token
        </Button>
      }
    >
      <QueryBoundary query={sessions} rows={2}>
        {(rows) => (
          <List>
            {rows.map((session) => (
              <ListRow
                key={session.id}
                title={session.name}
                badge={session.current ? <Badge tone="accent">This browser</Badge> : undefined}
                sub={sessionUsageLabel(session)}
                actions={
                  session.current ? undefined : (
                    <Button size="sm" variant="danger" onClick={() => revoke.ask(session)}>
                      Sign out
                    </Button>
                  )
                }
              />
            ))}
          </List>
        )}
      </QueryBoundary>

      <div className="setting-row__actions">
        <Button size="sm" variant="danger" onClick={() => everywhere.ask()}>
          Sign out everywhere
        </Button>
      </div>

      <NameDialog
        open={naming}
        onOpenChange={setNaming}
        title="New personal token"
        description="For a script or the MCP server. It acts as you, with everything you can do."
        placeholder="Backup script, MCP server, …"
        maxLength={100}
        submitLabel="Create token"
        onSubmit={(name) => create.mutateAsync(name).then(setMinted)}
      />

      <PersonalTokenDialog minted={minted} onClose={() => setMinted(null)} />

      <ConfirmDialog
        {...revoke.dialog}
        title={revoke.target ? `Sign out ${revoke.target.name}?` : 'Sign out this session?'}
        confirmLabel="Sign out"
      >
        <p>Whatever is using it will need to sign in again.</p>
      </ConfirmDialog>

      <ConfirmDialog {...everywhere.dialog} title="Sign out everywhere?" confirmLabel="Sign out everywhere">
        <p>
          Every browser and app, this one included, will need to sign in again, and every personal
          token stops working.
        </p>
      </ConfirmDialog>
    </Card>
  )
}

/** The one sight of a personal token: the server keeps only its digest. */
function PersonalTokenDialog({
  minted: openMinted,
  onClose,
}: {
  minted: PersonalToken | null
  onClose: () => void
}) {
  const minted = useHeld(openMinted)

  return (
    <Dialog
      open={openMinted !== null}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      {minted !== null ? (
        <DialogContent
          title="Copy this token now"
          description={`Shown only once. If lost, sign out ${minted.session.name} and create another.`}
          footer={
            <DialogActions cancel={false}>
              <Button variant="primary" onClick={onClose}>
                Done
              </Button>
            </DialogActions>
          }
        >
          <CopyableSecret value={minted.token} />
        </DialogContent>
      ) : null}
    </Dialog>
  )
}

/**
 * Where SSO stands. Linking happens only on the login page
 * (`OIDCLinkExistingEmail`), so this card points there.
 */
function SingleSignOnCard() {
  const { user } = useAuth()
  const config = useOidcConfig()

  return (
    <Card
      title="Single sign-on"
      subtitle="Signing in through your organization's identity provider."
    >
      <QueryBoundary query={config} rows={1}>
        {(data) => {
          if (!data.enabled) {
            return <p className="hint">Not configured on this server.</p>
          }
          if (user?.has_oidc) {
            return (
              <p className="hint">
                <Link2 size={14} aria-hidden="true" /> Linked to {data.provider_name}.
              </p>
            )
          }
          return (
            <p className="hint">
              Not linked. Sign in with {data.provider_name} from the login page to link it.
            </p>
          )
        }}
      </QueryBoundary>
    </Card>
  )
}
