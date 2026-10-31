import { useState } from 'react'

import { InfoTip } from '@/components/InfoTip'
import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  Input,
  OptionSelect,
} from '@/components/ui'
import {
  useCreateEmailConnection,
  useUpdateEmailConnection,
  type EmailConnection,
  type MailboxKind,
} from '@/lib/clients/email'

import { mailboxKindName, mailboxPort } from './mailbox'

/**
 * Add the mailbox billing mail is forwarded into, or correct the one there.
 * There is deliberately no password field: signing in is a separate button on
 * the card, the only place a secret is typed. The kind is chosen once, because
 * the sealed token describes one server.
 */
export function MailboxDialog({
  connection,
  onClose,
}: {
  /** The mailbox being edited, or null to add one. */
  connection: EmailConnection | null
  onClose: () => void
}) {
  const [kind, setKind] = useState<MailboxKind>(connection?.kind ?? 'graph')
  const [label, setLabel] = useState(connection?.label ?? '')
  const [address, setAddress] = useState(connection?.address ?? '')
  const [clientId, setClientId] = useState(connection?.client_id ?? '')
  const [tenant, setTenant] = useState(connection?.tenant ?? '')
  const [host, setHost] = useState(connection?.host ?? '')
  const [port, setPort] = useState(connection?.port === null ? '' : String(connection?.port ?? ''))
  const [username, setUsername] = useState(connection?.username ?? '')
  const [folder, setFolder] = useState(connection?.folder ?? '')

  const create = useCreateEmailConnection()
  const update = useUpdateEmailConnection()
  const pending = create.isPending || update.isPending

  const named = label.trim()
  const mailbox = address.trim()
  const chosenPort = mailboxPort(port)
  const ready =
    named !== '' &&
    mailbox !== '' &&
    chosenPort !== undefined &&
    (kind === 'graph'
      ? clientId.trim() !== '' && tenant.trim() !== ''
      : host.trim() !== '' && username.trim() !== '')

  const save = () => {
    if (!ready || chosenPort === undefined) return
    const shared = { label: named, address: mailbox, folder: folder.trim() }
    const details =
      kind === 'graph'
        ? { client_id: clientId.trim(), tenant: tenant.trim() }
        : { host: host.trim(), port: chosenPort, username: username.trim() }
    if (connection) {
      update.mutate({ id: connection.id, patch: { ...shared, ...details } }, { onSuccess: onClose })
    } else {
      create.mutate({ kind, ...shared, ...details }, { onSuccess: onClose })
    }
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent
        title={connection ? `Edit ${connection.label}` : 'Add a mailbox'}
        description="Where billing mail is forwarded. Read every half hour."
        onSubmit={(event) => {
          event.preventDefault()
          save()
        }}
        footer={
          <DialogActions>
            <Button type="submit" variant="primary" disabled={!ready || pending}>
              {connection ? 'Save' : 'Add mailbox'}
            </Button>
          </DialogActions>
        }
      >
        {connection ? null : (
          <Field label="Kind">
            <OptionSelect
              value={kind}
              onValueChange={(next) => setKind(asMailboxKind(next))}
              options={MAILBOX_KINDS.map((one) => ({ value: one, label: mailboxKindName(one) }))}
            />
          </Field>
        )}

        <Field
          label="Display name"
          hint="Shown on the card."
        >
          <Input
            value={label}
            onChange={(event) => setLabel(event.target.value)}
            placeholder="Bills"
            maxLength={80}
          />
        </Field>

        <Field
          label="Mailbox address"
          hint={
            kind === 'graph'
              ? 'A shared mailbox works if the signing-in account can read it.'
              : 'Usually also the username below.'
          }
        >
          <Input
            type="email"
            value={address}
            onChange={(event) => setAddress(event.target.value)}
            autoComplete="off"
          />
        </Field>

        {kind === 'graph' ? (
          <>
            <Field
              label="Application (client) id"
              hint={
                <>
                  From the Entra ID app registration&rsquo;s Overview. No client secret.{' '}
                  <InfoTip label="Registration requirements">
                    Public client flows on, with the delegated Mail.Read, Mail.Read.Shared and
                    offline_access permissions.
                  </InfoTip>
                </>
              }
            >
              <Input
                value={clientId}
                onChange={(event) => setClientId(event.target.value)}
                autoComplete="off"
              />
            </Field>
            <Field
              label="Directory (tenant) id"
              hint="Same Overview page."
            >
              <Input
                value={tenant}
                onChange={(event) => setTenant(event.target.value)}
                autoComplete="off"
              />
            </Field>
          </>
        ) : (
          <>
            <div className="form-row">
              <Field label="Server" hint="Gmail is imap.gmail.com.">
                <Input
                  value={host}
                  onChange={(event) => setHost(event.target.value)}
                  placeholder="imap.gmail.com"
                  autoComplete="off"
                />
              </Field>
              <Field
                label="Port"
                hint="993 unless the server says otherwise."
                error={chosenPort === undefined ? 'Not a port' : undefined}
              >
                <Input
                  numeric
                  inputMode="numeric"
                  value={port}
                  onChange={(event) => setPort(event.target.value)}
                  placeholder="993"
                />
              </Field>
            </div>
            <Field
              label="Username"
              hint="The password is asked for on the card after saving. Gmail needs an app password (2-Step Verification on)."
            >
              <Input
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                autoComplete="off"
              />
            </Field>
          </>
        )}

        <Field
          label="Folder"
          hint={
            kind === 'graph'
              ? 'Usually Inbox.'
              : 'INBOX, or a Gmail label by name.'
          }
        >
          <Input
            value={folder}
            onChange={(event) => setFolder(event.target.value)}
            placeholder={kind === 'graph' ? 'Inbox' : 'INBOX'}
            maxLength={200}
          />
        </Field>
      </DialogContent>
    </Dialog>
  )
}

/** Office 365 first: it is the kind a household most often forwards billing mail into. */
const MAILBOX_KINDS: readonly MailboxKind[] = ['graph', 'imap']

function asMailboxKind(value: string): MailboxKind {
  return MAILBOX_KINDS.find((one) => one === value) ?? 'graph'
}
