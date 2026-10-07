import { LogIn, Mail, Pencil, Trash2, Unplug } from 'lucide-react'
import { useEffect, useEffectEvent, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import { QueryBoundary } from '@/components/QueryBoundary'
import { useSignIns } from '@/components/signin/signInTasks-context'
import {
  Badge,
  Card,
  ConfirmDialog,
  EmptyState,
  type OverflowAction,
  useConfirm,
  useProgressToast,
} from '@/components/ui'
import {
  describePollStatus,
  mailboxFolder,
  useDeleteEmailConnection,
  useEmailConnections,
  useForgetMailboxSecret,
  usePollMailbox,
  type EmailConnection,
} from '@/lib/clients/email'

import { RefreshButton, SignInButton } from '../connector/ConnectorControls'
import { ConnectorList, ConnectorRow } from '../connector/ConnectorRow'
import { ForgetSignInConfirm } from '../connector/ForgetSignInConfirm'
import { useForgetSignIn } from '../connector/useForgetSignIn'
import { describePollResult, mailboxBadge, mailboxKindName } from './mailbox'
import { MailboxDialog } from './MailboxDialog'

/**
 * The mailboxes the household forwards its mail into: providers that bill by
 * email land here, and sign-in codes are answered out of them.
 */
export function MailboxCard() {
  const connections = useEmailConnections()
  const remove = useConfirm(useDeleteEmailConnection(), {
    variables: (connection: EmailConnection) => connection.id,
  })

  const [params, setParams] = useSearchParams()
  const [editing, setEditing] = useState<EmailConnection | null>(null)
  // The shell's rather than this card's, so the device-code wait carries on
  // while somebody is off in another tab entering the code.
  const signIns = useSignIns()

  // `?sign-in=<mailbox>` is the link the assistant hands on, since only a
  // person may sign in to a mailbox. It opens that mailbox's sign-in once.
  const signInFor = params.get('sign-in')
  const signingIn =
    signInFor === null ? null : (connections.data?.find((one) => one.id === signInFor) ?? null)
  const openSignIn = useEffectEvent((connection: EmailConnection) => {
    signIns.open({ kind: 'mailbox', connection })
    const next = new URLSearchParams(params)
    next.delete('sign-in')
    setParams(next, { replace: true })
  })
  useEffect(() => {
    if (signingIn !== null) openSignIn(signingIn)
  }, [signingIn])

  return (
    <>
      <Card
        title="Mailboxes"
        subtitle="Forward bills, receipts and sign-in codes here. Bills file themselves; codes are answered before they expire."
      >
        <QueryBoundary
          query={connections}
          rows={2}
          empty={(rows) =>
            rows.length === 0 ? (
              <EmptyState
                compact
                title="No mailbox yet. Add one and forward bills and receipts into it; it is read every half hour."
              />
            ) : undefined
          }
        >
          {(rows) => (
            <ConnectorList>
              {rows.map((connection) => (
                <MailboxRow
                  key={connection.id}
                  connection={connection}
                  onEdit={() => setEditing(connection)}
                  onDelete={() => remove.ask(connection)}
                  onSignIn={() => signIns.open({ kind: 'mailbox', connection })}
                />
              ))}
            </ConnectorList>
          )}
        </QueryBoundary>
      </Card>

      {editing ? (
        <MailboxDialog
          connection={editing}
          onClose={() => setEditing(null)}
        />
      ) : null}

      <ConfirmDialog
        {...remove.dialog}
        title={remove.target ? `Remove ${remove.target.label}?` : 'Remove this mailbox?'}
        description="Its sign-in and read log are deleted. Bills already filed stay, and the mailbox itself is untouched."
        confirmLabel="Remove mailbox"
      />
    </>
  )
}

/**
 * One mailbox row. A connected mailbox keeps "Sign in again" in its menu: a
 * revoked consent and an expired refresh token are both fixed by that flow.
 */
function MailboxRow({
  connection,
  onEdit,
  onDelete,
  onSignIn,
}: {
  connection: EmailConnection
  onEdit: () => void
  onDelete: () => void
  onSignIn: () => void
}) {
  const progress = useProgressToast()
  const poll = usePollMailbox()
  const forget = useForgetSignIn(useForgetMailboxSecret())
  const badge = mailboxBadge(connection)
  const title = `${mailboxKindName(connection.kind)} · ${connection.label}`

  const readNow = () =>
    void progress(
      `Reading ${connection.label}…`,
      poll.mutateAsync(connection.id),
      (result) => ({
        title: describePollResult(result),
        tone: result.error === '' ? undefined : 'error',
      }),
      `${connection.label} was not read`,
    )

  const actions: OverflowAction[] = connection.connected
    ? [
        { label: 'Sign in again', icon: <LogIn size={14} />, onSelect: onSignIn },
        {
          label: 'Forget sign-in',
          icon: <Unplug size={14} />,
          disabled: forget.dialog.pending,
          onSelect: () => forget.ask({ id: connection.id, name: connection.label }),
        },
      ]
    : []

  return (
    <ConnectorRow
      title={title}
      badge={<Badge tone={badge.tone}>{badge.word}</Badge>}
      meta={[
        <>
          <Mail size={12} aria-hidden="true" /> {connection.address} · {mailboxFolder(connection)}
        </>,
        describePollStatus(connection),
      ]}
      primary={
        connection.connected ? (
          <RefreshButton
            busy={poll.isPending}
            label="Read now"
            busyLabel="Reading…"
            onClick={readNow}
          />
        ) : (
          <SignInButton label="Sign in" onClick={onSignIn} />
        )
      }
      actions={[
        ...actions,
        { label: 'Edit', icon: <Pencil size={14} />, onSelect: onEdit },
        { label: 'Remove', icon: <Trash2 size={14} />, danger: true, onSelect: onDelete },
      ]}
      actionsLabel={`Actions for ${title}`}
    >
      <ForgetSignInConfirm confirm={forget} kept="Filed bills and the read log" />
    </ConnectorRow>
  )
}
