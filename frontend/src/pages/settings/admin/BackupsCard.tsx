import { DatabaseBackup, Download, KeyRound, Plus, RotateCcw, Trash2 } from 'lucide-react'
import { useState, type ReactNode } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Callout,
  Card,
  Checkbox,
  ConfirmDialog,
  CopyableSecret,
  CopyButton,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  FileInput,
  Input,
  OverflowMenu,
  RowActions,
  Spinner,
  Table,
  TableEmptyRow,
  Td,
  Th,
} from '@/components/ui'
import { generateAgeKeyPair, identityFile, type AgeKeyPair } from '@/lib/ageKeys'
import { describeRecipient, isSSHPrivateKey, RECIPIENT_KIND_LABELS } from '@/lib/backupKeys'
import {
  backupSettingsBody,
  useBackups,
  useRehearseBackup,
  useRunBackup,
  useSaveBackupSettings,
  withoutRecipient,
  withRecipient,
  type BackupRehearsal,
  type BackupRun,
  type BackupSet,
  type Backups,
} from '@/lib/clients/admin'
import { formatSize } from '@/lib/clients/attachments'
import { describeApiError } from '@/lib/errors'
import { formatTimestamp, plural } from '@/lib/format'
import { backupKeyUsername, storePasswordCredential } from '@/lib/passwordManager'
import { saveBlob } from '@/lib/saveFile'

import {
  confirmsRestore,
  IDENTITY_FILE_NAME,
  NOT_A_RECIPIENT,
  recipientSummary,
  restoreCommand,
  shortKey,
  triggerLabel,
} from './backupText'

/**
 * The server's own backups: when they run, how long they are kept, whom they
 * are encrypted to, what is on disk, and a way back from any of it. Only the
 * public half of a key ever reaches the server; an identity made or pasted
 * here lives in this tab until its dialog closes.
 */
export function BackupsCard() {
  const backups = useBackups()
  return (
    <Card title="Backups" subtitle="The database, the attachments and the secrets, once a night.">
      <QueryBoundary query={backups} rows={4}>
        {(stored) => <BackupsBody backups={stored} />}
      </QueryBoundary>
    </Card>
  )
}

export function BackupsBody({ backups }: { backups: Backups }) {
  const [restoring, setRestoring] = useState<BackupSet | null>(null)
  return (
    <div className="stack">
      <BackupNotices backups={backups} />
      <RunStatus backups={backups} />
      <ScheduleForm key={`${backups.at}-${backups.keep_days}`} backups={backups} />
      <Field
        label="Backup directory"
        hint={
          'A bind mount set in docker-compose.yml (the host’s ./backups next to it by default), ' +
          'so it is changed there, not here. Have the machine’s own nightly backup include it.'
        }
      >
        <code className="admin-secret__value">{backups.directory || 'Not set (BACKUP_DIR)'}</code>
      </Field>
      <Recipients backups={backups} />
      <SetsTable backups={backups} onRestore={setRestoring} />
      <RestoreDialog set={restoring} onClose={() => setRestoring(null)} />
    </div>
  )
}

export function BackupNotices({ backups }: { backups: Backups }) {
  if (!backups.enabled) {
    return (
      <Callout title="Backups are off">
        BACKUP_DIR is not set, so this server writes no backups. The compose file sets it.
      </Callout>
    )
  }
  return (
    <>
      {backups.encrypted ? null : (
        <Callout tone="warning" title="Backups are being written unencrypted">
          Anybody with a copy of the backups directory, or of this machine’s own backup, can read
          every account, transaction and stored connection in them. Generate a key pair below:
          the server keeps only the public half, so the sets cannot be read without the key you
          keep somewhere else.
        </Callout>
      )}
      {backups.problem ? (
        <Callout tone="expense" title="A backup cannot run">
          {backups.problem}
        </Callout>
      ) : null}
    </>
  )
}

function runSentence(run: BackupRun): string {
  const when = formatTimestamp(run.started_at)
  switch (run.status) {
    case 'running':
      return `Running since ${when}.`
    case 'succeeded':
      return `${when}: wrote ${run.set_name ?? 'a set'}, ${formatSize(run.bytes)}, ${
        run.encrypted ? 'encrypted' : 'unencrypted'
      }.`
    case 'failed':
      return `${when}: failed.`
    case 'skipped':
      return `${when}: could not run.`
  }
}

function RunStatus({ backups }: { backups: Backups }) {
  const run = useRunBackup()
  const last = backups.runs[0]
  const busy = backups.running || run.isPending
  return (
    <div className="setting-row">
      <div className="setting-row__text">
        <p className="setting-row__label">Last run</p>
        <p className="hint">{last ? runSentence(last) : 'None yet.'}</p>
        {last?.error ? (
          <Callout tone={last.status === 'failed' ? 'expense' : 'warning'}>{last.error}</Callout>
        ) : null}
        {backups.next_run ? (
          <p className="hint">Next nightly run {formatTimestamp(backups.next_run)}.</p>
        ) : null}
      </div>
      <Button
        variant="primary"
        disabled={!backups.enabled || busy}
        onClick={() => run.mutate()}
      >
        {busy ? <Spinner /> : <DatabaseBackup size={13} aria-hidden="true" />}
        {busy ? 'Backing up…' : 'Back up now'}
      </Button>
    </div>
  )
}

function ScheduleForm({ backups }: { backups: Backups }) {
  const save = useSaveBackupSettings()
  const [at, setAt] = useState(backups.at)
  const [keepDays, setKeepDays] = useState(String(backups.keep_days))
  const fromEnvironment = (field: string) =>
    backups.sources[field] === 'environment' ? ' Coming from the environment.' : ''
  const days = Number(keepDays)
  const ready = /^\d{2}:\d{2}$/.test(at) && Number.isInteger(days) && days >= 1
  return (
    <div className="import__actions">
      <Field label="Nightly at" hint={`In ${backups.timezone}.${fromEnvironment('at')}`}>
        <Input type="time" value={at} onChange={(event) => setAt(event.target.value)} />
      </Field>
      <Field label="Keep for (days)" hint={`The newest set is always kept.${fromEnvironment('keep_days')}`}>
        <Input
          type="number"
          min={1}
          numeric
          value={keepDays}
          onChange={(event) => setKeepDays(event.target.value)}
        />
      </Field>
      <Button
        disabled={!ready || save.isPending}
        onClick={() => save.mutate(backupSettingsBody(backups, { at, keep_days: days }))}
      >
        {save.isPending ? 'Saving…' : 'Save schedule'}
      </Button>
    </div>
  )
}

function Recipients({ backups }: { backups: Backups }) {
  const save = useSaveBackupSettings()
  const [adding, setAdding] = useState(false)
  const [generating, setGenerating] = useState(false)
  const [removing, setRemoving] = useState<string | null>(null)
  const last = backups.recipients.length === 1
  return (
    <Field
      as="group"
      label="Encrypted to"
      hint={
        'Each set can be opened by any one of these keys. Lose every private key and the ' +
        'encrypted backups cannot be read by anybody, including you.' +
        (backups.sources.recipients === 'environment' && backups.recipients.length > 0
          ? ' Coming from the environment.'
          : '')
      }
    >
      {backups.recipients.length === 0 ? (
        <p className="muted">No keys. Sets are written unencrypted.</p>
      ) : (
        <ul className="admin-keys">
          {backups.recipients.map((one) => (
            <li key={one.recipient} className="admin-secret">
              <span className="admin-user__name">
                <KeyRound size={13} aria-hidden="true" />
                {one.label || 'Unlabelled key'}
                <Badge>{RECIPIENT_KIND_LABELS[one.kind] ?? 'Unreadable'}</Badge>
              </span>
              <code title={one.fingerprint || one.recipient}>{shortKey(one.fingerprint || one.recipient)}</code>
              {one.added_at && !one.added_at.startsWith('0001') ? (
                <span className="cell__sub">Added {formatTimestamp(one.added_at, 'date')}</span>
              ) : null}
              <CopyButton text={one.recipient} variant="ghost" size="sm" />
              <Button variant="ghost" size="sm" onClick={() => setRemoving(one.recipient)}>
                <Trash2 size={13} aria-hidden="true" /> Remove
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="setting-row__actions">
        <Button variant="primary" size="sm" onClick={() => setGenerating(true)}>
          <KeyRound size={13} aria-hidden="true" /> Generate a key pair
        </Button>
        <Button size="sm" onClick={() => setAdding(true)}>
          <Plus size={13} aria-hidden="true" /> Add a public key
        </Button>
      </div>

      <Dialog open={generating} onOpenChange={setGenerating}>
        {generating ? <GenerateKeyContent backups={backups} onDone={() => setGenerating(false)} /> : null}
      </Dialog>
      <Dialog open={adding} onOpenChange={setAdding}>
        {adding ? <AddKeyContent backups={backups} onDone={() => setAdding(false)} /> : null}
      </Dialog>
      <ConfirmDialog
        open={removing !== null}
        title="Remove this key?"
        description={
          last
            ? 'It is the only one. Every set from the next run on is written unencrypted.'
            : 'Sets already written stay encrypted to it; new ones are not.'
        }
        confirmLabel="Remove"
        pending={save.isPending}
        onCancel={() => setRemoving(null)}
        onConfirm={() => {
          if (removing === null) return
          save.mutate(withoutRecipient(backups, removing), { onSuccess: () => setRemoving(null) })
        }}
      />
    </Field>
  )
}

/**
 * The private half, shown once. It is made when the dialog opens and goes
 * with the dialog; the server is sent the public half only.
 */
function GenerateKeyContent({ backups, onDone }: { backups: Backups; onDone: () => void }) {
  const [pair] = useState<AgeKeyPair>(generateAgeKeyPair)
  const [createdAt] = useState(() => new Date())
  const [label, setLabel] = useState(`Generated ${formatTimestamp(createdAt.toISOString(), 'date')}`)
  const [stored, setStored] = useState(false)
  const save = useSaveBackupSettings()
  return (
    <DialogContent
      title="A new backup key"
      description="Made in this browser. Store the private key now: it is not shown again, and the server never has it."
      footer={
        <DialogActions>
          <Button
            variant="primary"
            disabled={!stored || save.isPending}
            onClick={() =>
              save.mutate(withRecipient(backups, pair.recipient, label), { onSuccess: onDone })
            }
          >
            {save.isPending ? 'Saving…' : 'Save the public key'}
          </Button>
        </DialogActions>
      }
    >
      <div className="stack">
        <Field label="Private key (identity)" hint="This opens every set encrypted to it. Keep it off this server.">
          <CopyableSecret value={pair.identity} />
        </Field>
        <SaveToPasswordManager secret={pair.identity} host={currentHost()}>
          <Button
            onClick={() =>
              saveBlob(
                new Blob([identityFile(pair, createdAt)], { type: 'text/plain;charset=utf-8' }),
                IDENTITY_FILE_NAME,
              )
            }
          >
            <Download size={13} aria-hidden="true" /> Download {IDENTITY_FILE_NAME}
          </Button>
        </SaveToPasswordManager>
        <Field label="Public key" hint="What the server encrypts to.">
          <code className="admin-secret__value">{pair.recipient}</code>
        </Field>
        <Field label="Label">
          <Input value={label} maxLength={80} onChange={(event) => setLabel(event.target.value)} />
        </Field>
        <Callout tone="warning">
          Lose this key and the backups encrypted to it cannot be read by anybody.
        </Callout>
        <Checkbox
          checked={stored}
          onCheckedChange={(next) => setStored(next === true)}
          label="I have stored this key somewhere other than this server"
        />
      </div>
    </DialogContent>
  )
}

function currentHost(): string {
  return typeof window === 'undefined' ? 'agentifi' : window.location.host
}

/**
 * The private key as a sign-up form, for the browser or an extension such as
 * Bitwarden to offer to save. Submitting takes the fields off the page, which
 * is how password managers tell a sign-up that went through; where the
 * browser allows it, its own manager is also asked directly. `children` are
 * the other ways to keep the key, drawn in the same row of buttons.
 */
export function SaveToPasswordManager({
  secret,
  host,
  children,
}: {
  secret: string
  host: string
  children?: ReactNode
}) {
  const [username, setUsername] = useState(() => backupKeyUsername(host))
  const [submitted, setSubmitted] = useState(false)
  return (
    <form
      className="stack stack--3"
      method="post"
      onSubmit={(event) => {
        event.preventDefault()
        void storePasswordCredential(window, username, secret, 'Agentifi backup key')
        setSubmitted(true)
      }}
    >
      {submitted ? (
        <Callout tone="info" title="Offered to your password manager">
          Accept its prompt to save the key as {username}; rehearsing a restore fills it back in
          from there. If nothing asked, copy the key or download the file instead.
        </Callout>
      ) : (
        <div className="import__actions">
          <Field label="Save as" hint="The login name in your password manager.">
            <Input
              name="username"
              autoComplete="username"
              spellCheck={false}
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </Field>
          <Field label="Private key">
            <Input
              type="password"
              name="password"
              autoComplete="new-password"
              value={secret}
              onChange={() => undefined}
            />
          </Field>
        </div>
      )}
      <div className="setting-row__actions">
        {submitted ? (
          <Button onClick={() => setSubmitted(false)}>
            <KeyRound size={13} aria-hidden="true" /> Offer it again
          </Button>
        ) : (
          <Button type="submit" disabled={username.trim() === ''}>
            <KeyRound size={13} aria-hidden="true" /> Save to password manager
          </Button>
        )}
        {children}
      </div>
    </form>
  )
}

function AddKeyContent({ backups, onDone }: { backups: Backups; onDone: () => void }) {
  const [recipient, setRecipient] = useState('')
  const [label, setLabel] = useState('')
  const save = useSaveBackupSettings()
  const described = describeRecipient(recipient)
  const valid = described !== null
  return (
    <DialogContent
      title="Add a public key"
      description={
        'An age public key, from age-keygen or another install, or an SSH public key ' +
        '(ssh-ed25519, or ssh-rsa of 2048 bits or more) whose private key you already keep. ' +
        'Sets are encrypted to it from the next run.'
      }
      onSubmit={() => {
        if (valid) save.mutate(withRecipient(backups, recipient, label), { onSuccess: onDone })
      }}
      footer={
        <DialogActions>
          <Button type="submit" variant="primary" disabled={!valid || save.isPending}>
            {save.isPending ? 'Saving…' : 'Add'}
          </Button>
        </DialogActions>
      }
    >
      <Field
        label="Public key"
        hint={described ? recipientSummary(described) : undefined}
        error={recipient.trim() !== '' && !valid ? NOT_A_RECIPIENT : undefined}
      >
        <Input
          value={recipient}
          placeholder="age1… or ssh-ed25519 AAAA…"
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => setRecipient(event.target.value)}
        />
      </Field>
      <Field
        label="Label"
        hint={
          described?.comment
            ? 'Where the private half is kept, for whoever needs it. Left empty, the key’s comment.'
            : 'Where the private half is kept, for whoever needs it.'
        }
      >
        <Input
          value={label}
          maxLength={80}
          placeholder={described?.comment || undefined}
          onChange={(event) => setLabel(event.target.value)}
        />
      </Field>
    </DialogContent>
  )
}

function SetsTable({ backups, onRestore }: { backups: Backups; onRestore: (set: BackupSet) => void }) {
  return (
    <Table density="sm" lines={2} stack>
      <thead>
        <tr>
          <Th>Set</Th>
          <Th>Size</Th>
          <Th>State</Th>
          <Th />
        </tr>
      </thead>
      <tbody>
        {backups.sets.length === 0 ? (
          <TableEmptyRow colSpan={4}>No backup sets on disk yet.</TableEmptyRow>
        ) : null}
        {backups.sets.map((set) => (
          <tr key={set.name}>
            <Td label="">
              <span className="admin-user__name">
                <span className="cell__clip" title={set.name}>
                  {formatTimestamp(set.created_at)}
                </span>
              </span>
              <span className="cell__sub cell__clip" title={set.name}>
                {triggerLabel(set.trigger)} · {set.name}
              </span>
            </Td>
            <Td label="Size" className="stack-inline">
              {formatSize(set.bytes)}
            </Td>
            <Td label="State" className="stack-inline">
              <span className="admin-user__name">
                {set.encrypted ? (
                  <Badge tone="accent">Encrypted</Badge>
                ) : (
                  <Badge tone="warning">Unencrypted</Badge>
                )}
                {set.verified ? <Badge>Verified</Badge> : null}
              </span>
              {set.intact ? null : <span className="cell__sub">Not intact: {set.problem}</span>}
              {set.key_matches === false ? (
                <span className="cell__sub">Its stored connections were sealed with another key.</span>
              ) : null}
            </Td>
            <Td numeric label="">
              <RowActions>
                <OverflowMenu
                  label={`Actions for ${set.name}`}
                  actions={[
                    {
                      label: 'Restore…',
                      icon: <RotateCcw size={14} />,
                      disabled: !set.intact,
                      onSelect: () => onRestore(set),
                    },
                  ]}
                />
              </RowActions>
            </Td>
          </tr>
        ))}
      </tbody>
    </Table>
  )
}

function RestoreDialog({ set, onClose }: { set: BackupSet | null; onClose: () => void }) {
  return (
    <Dialog open={set !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      {set ? <RestoreContent set={set} /> : null}
    </Dialog>
  )
}

/**
 * Rehearsing is done here, against a scratch file; restoring is handed to
 * the host. The identity pasted for a rehearsal is sent with that one request
 * and goes when the dialog closes.
 */
function RestoreContent({ set }: { set: BackupSet }) {
  const rehearse = useRehearseBackup()
  const [identity, setIdentity] = useState('')
  const [passphrase, setPassphrase] = useState('')
  const [fileName, setFileName] = useState<string | null>(null)
  const [typed, setTyped] = useState('')
  const ready = !rehearse.isPending && (!set.encrypted || identity.trim() !== '')
  return (
    <DialogContent
      wide
      title={`Restore ${set.name}`}
      description={`Taken ${formatTimestamp(set.created_at)} (${triggerLabel(set.trigger)}).`}
      footer={<DialogActions cancel="Close" />}
    >
      <div className="stack">
        <section className="stack stack--3">
          <h3 className="setting-row__label">Rehearse</h3>
          <p className="hint">
            Restores the set into a scratch file beside the live database, counts its rows, reads its
            attachments through and removes the scratch file. Nothing live changes.
          </p>
          <form
            className="settings__form"
            method="post"
            onSubmit={(event) => {
              event.preventDefault()
              if (ready) rehearse.mutate({ name: set.name, identity, passphrase })
            }}
          >
            {set.encrypted ? (
              <IdentityFields
                host={currentHost()}
                identity={identity}
                onIdentityChange={setIdentity}
                passphrase={passphrase}
                onPassphraseChange={setPassphrase}
                fileName={fileName}
                onFileNameChange={setFileName}
              />
            ) : null}
            <div className="setting-row__actions">
              <Button type="submit" disabled={!ready}>
                {rehearse.isPending ? <Spinner /> : null}
                {rehearse.isPending ? 'Rehearsing…' : 'Rehearse the restore'}
              </Button>
            </div>
          </form>
          {rehearse.isError ? (
            <Callout tone="expense">{describeApiError(rehearse.error)}</Callout>
          ) : null}
          {rehearse.data ? <RehearsalReport report={rehearse.data} /> : null}
        </section>

        <section className="stack stack--3">
          <h3 className="setting-row__label">Restore</h3>
          <RestoreHandoff name={set.name} typed={typed} onTypedChange={setTyped} />
        </section>
      </div>
    </DialogContent>
  )
}

/**
 * The identity as a sign-in form, under the login a generated key was saved
 * with, so a password manager fills it. An SSH private key comes through the
 * file, or pasted: the server mends the line breaks a single-line field drops.
 */
export function IdentityFields({
  host,
  identity,
  onIdentityChange,
  passphrase,
  onPassphraseChange,
  fileName,
  onFileNameChange,
}: {
  host: string
  identity: string
  onIdentityChange: (identity: string) => void
  passphrase: string
  onPassphraseChange: (passphrase: string) => void
  fileName: string | null
  onFileNameChange: (fileName: string) => void
}) {
  const [username, setUsername] = useState(() => backupKeyUsername(host))
  return (
    <>
      <Field label="Saved as" hint="The login name the key has in your password manager, if it is in one.">
        <Input
          name="username"
          autoComplete="username"
          spellCheck={false}
          value={username}
          onChange={(event) => setUsername(event.target.value)}
        />
      </Field>
      <Field
        label="Identity"
        hint="The private key: an age key (AGE-SECRET-KEY-1…) or an SSH private key. It is not kept."
      >
        <Input
          type="password"
          name="password"
          autoComplete="current-password"
          placeholder="AGE-SECRET-KEY-1…"
          spellCheck={false}
          value={identity}
          onChange={(event) => onIdentityChange(event.target.value)}
        />
      </Field>
      <Field label="Or the key file">
        <FileInput
          fileName={fileName}
          onChange={(event) => {
            const file = event.target.files?.[0]
            if (!file) return
            onFileNameChange(file.name)
            void file.text().then(onIdentityChange)
          }}
        />
      </Field>
      {isSSHPrivateKey(identity) ? (
        <Field label="Passphrase" hint="The SSH key’s passphrase; empty when it has none. It is not kept.">
          <Input
            type="password"
            name="passphrase"
            autoComplete="off"
            value={passphrase}
            onChange={(event) => onPassphraseChange(event.target.value)}
          />
        </Field>
      ) : null}
    </>
  )
}

export function RehearsalReport({ report }: { report: BackupRehearsal }) {
  const tables = report.tables.filter((table) => table.rows > 0)
  return (
    <div className="settings__form">
      <Callout tone="info" title="The rehearsal restored cleanly">
        {plural(report.rows, 'row')} in {plural(report.tables.length, 'table')},{' '}
        {plural(report.attachments, 'attachment')}.
        {report.secrets.length > 0 ? ` Secrets in the set: ${report.secrets.join(', ')}.` : ''}
      </Callout>
      {report.warnings.map((warning) => (
        <Callout key={warning} tone="warning">
          {warning}
        </Callout>
      ))}
      <Table density="sm">
        <thead>
          <tr>
            <Th>Table</Th>
            <Th numeric>Rows</Th>
          </tr>
        </thead>
        <tbody>
          {tables.map((table) => (
            <tr key={table.table}>
              <Td>{table.table}</Td>
              <Td numeric>{table.rows}</Td>
            </tr>
          ))}
        </tbody>
      </Table>
    </div>
  )
}

/**
 * A restore swaps the database out from under the running server: its syncs,
 * imports and automations would go on writing into the one being replaced,
 * and it has to start again on the restored schema. So it runs on the host
 * with the application stopped, and this hands over the command once the
 * set's name has been typed back.
 */
export function RestoreHandoff({
  name,
  typed,
  onTypedChange,
}: {
  name: string
  typed: string
  onTypedChange: (typed: string) => void
}) {
  const confirmed = confirmsRestore(typed, name)
  return (
    <>
      <p className="hint">
        Restoring overwrites the live database and attachments, so it runs on the host with the
        application stopped rather than from this page: the running server would go on writing
        syncs, imports and automations into the database being replaced, and has to start again
        on the restored one. It backs up the current database first, restores into a new database
        all or nothing, swaps it in, then applies migrations.
      </p>
      <Field label={`Type ${name} to show the command`}>
        <Input
          value={typed}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => onTypedChange(event.target.value)}
        />
      </Field>
      {confirmed ? (
        <Field
          label="Run on the host, beside docker-compose.yml"
          hint={
            `With the key file saved there as ${IDENTITY_FILE_NAME}, or an SSH private key in its ` +
            'place. For an SSH key with a passphrase, export BACKUP_IDENTITY_PASSPHRASE and add ' +
            '-e BACKUP_IDENTITY_PASSPHRASE after -T.'
          }
        >
          <CopyableSecret value={restoreCommand(name)} />
        </Field>
      ) : null}
    </>
  )
}
