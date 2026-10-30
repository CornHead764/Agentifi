import { useState, type ReactNode } from 'react'
import { Ban, ChevronDown, ChevronRight, Undo2 } from 'lucide-react'

import { Money } from '@/components/Money'
import {
  Badge,
  Button,
  CollapsibleCard,
  ConfirmDialog,
  EmptyState,
  List,
  ListRow,
  SkeletonRows,
  useConfirm,
  useToast,
} from '@/components/ui'
import { accountDisplayName, maskedNumber } from '@/lib/accounts'
import { useRestoreRemoteAccount, type Connection } from '@/lib/clients/connections'
import {
  emptyByInstitution,
  useIgnoreEmptyAccounts,
  useIgnoredAccounts,
  useUnignoreAccount,
  type EmptyAtInstitution,
} from '@/lib/clients/ignoredAccounts'
import { useInstitutions } from '@/lib/clients/institutions'
import { plural } from '@/lib/format'
import { useAccounts } from '@/lib/transactions/queries'

import { groupIgnored, remoteIgnoredRows, type IgnoredRow } from './ignoredRows'

/**
 * Everything the household has ignored: its own ignored accounts, and the
 * accounts each connection was told not to import. An ignored account leaves
 * every list and figure and stops syncing until put back.
 */
export function IgnoredAccountsCard({
  connections,
}: {
  connections: Connection[]
}) {
  const { show } = useToast()
  const ignored = useIgnoredAccounts()
  const accounts = useAccounts()
  const institutions = useInstitutions()
  const ignoreEmpty = useConfirm(useIgnoreEmptyAccounts(), {
    variables: (offer: EmptyAtInstitution) => offer.id,
    onSuccess: (result, offer) =>
      show({
        title: `Ignored ${plural(result.account_ids.length, 'account')} at ${offer.name}`,
        tone: 'success',
      }),
  })
  const unignore = useUnignoreAccount()
  const restore = useRestoreRemoteAccount()

  const names = new Map((institutions.data ?? []).map((one) => [one.id, one.name]))
  const offers = emptyByInstitution(accounts.data ?? [], names)
  const rows: IgnoredRow[] = [
    ...(ignored.data ?? []).map((row) => ({
      key: row.id,
      name: accountDisplayName(row.name),
      institution: row.institution ?? '',
      details: [maskedNumber(row.masked_number)],
      balance: row.balance,
      restore: () => unignore.mutateAsync(row.id),
    })),
    ...connections.flatMap((connection) =>
      remoteIgnoredRows(
        connection,
        (id, ignoredId) => restore.mutateAsync({ id, ignoredId }),
        'Not synced from SimpleFIN',
      ),
    ),
  ]

  return (
    <CollapsibleCard
      id="ignored-accounts"
      storageKey="settings.ignored-accounts.collapsed"
      startCollapsed
      what="ignored accounts"
      title={
        <>
          Ignored accounts{' '}
          {ignored.isSuccess ? <Badge count>{rows.length}</Badge> : null}
        </>
      }
      subtitle="Out of every list, total, report and plan, and not synced. Stop ignoring one to bring it back as it was."
    >
      {offers.length > 0 ? (
        <div className="ignored ignored--own-card">
          <p className="ignored__title">Accounts holding nothing</p>
          <p className="hint">
            Ignoring these reaches only the accounts at zero when you ignore them. One that empties later
            stays where it is.
          </p>
          <List>
            {offers.map((offer) => (
              <ListRow
                key={offer.id}
                title={offer.name}
                sub={`${plural(offer.count, 'account')} at zero`}
                actions={
                  <Button size="sm" disabled={ignoreEmpty.dialog.pending} onClick={() => ignoreEmpty.ask(offer)}>
                    <Ban size={13} aria-hidden="true" /> Ignore {offer.count === 1 ? 'it' : 'them'}
                  </Button>
                }
              />
            ))}
          </List>
        </div>
      ) : null}

      {ignored.isPending ? <SkeletonRows rows={2} /> : null}
      {ignored.isSuccess && rows.length === 0 ? (
        <EmptyState compact title="No accounts are ignored. Ignore one from its menu above." />
      ) : null}
      {rows.length > 0 ? (
        <IgnoredList
          title={offers.length > 0 ? 'Ignored' : undefined}
          rows={rows}
          onRestored={(count) =>
            show({
              title: count === 1 ? 'The account is back' : `${plural(count, 'account')} are back`,
              tone: 'success',
            })
          }
          ownCard
        />
      ) : null}

      <ConfirmDialog
        {...ignoreEmpty.dialog}
        title={
          ignoreEmpty.target
            ? `Ignore ${plural(ignoreEmpty.target.count, 'account')} at ${ignoreEmpty.target.name}?`
            : 'Ignore empty accounts?'
        }
        description="They leave every list and total and stop syncing. You can put any of them back from this card."
        confirmLabel="Ignore"
      />
    </CollapsibleCard>
  )
}

/**
 * Ignored accounts by institution, each with a way back, so nothing silently
 * disappears. An institution with several folds them under one row.
 */
export function IgnoredList({
  title,
  rows,
  onRestored,
  ownCard = false,
}: {
  title?: ReactNode
  rows: IgnoredRow[]
  /** Called with how many came back, once every one asked for has answered. */
  onRestored?: (count: number) => void
  ownCard?: boolean
}) {
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set())
  const [busy, setBusy] = useState(false)

  const bringBack = (members: readonly IgnoredRow[]) => {
    setBusy(true)
    void Promise.allSettled(members.map((row) => row.restore())).then((results) => {
      setBusy(false)
      const back = results.filter((result) => result.status === 'fulfilled').length
      if (back > 0) onRestored?.(back)
    })
  }
  const toggle = (institution: string) =>
    setOpen((current) => {
      const next = new Set(current)
      if (!next.delete(institution)) next.add(institution)
      return next
    })
  const restoreButton = (members: readonly IgnoredRow[], label: string) => (
    <Button size="sm" disabled={busy} onClick={() => bringBack(members)}>
      <Undo2 size={13} aria-hidden="true" /> {label}
    </Button>
  )

  return (
    <div className={ownCard ? 'ignored ignored--own-card' : 'ignored'}>
      {title ? <p className="ignored__title">{title}</p> : null}
      <List>
        {groupIgnored(rows).flatMap((group) => {
          const institution = group.institution || 'No institution'
          if (group.rows.length === 1) {
            const [row] = group.rows
            return [
              <ListRow
                key={row.key}
                title={row.name}
                sub={details([institution, ...row.details])}
                figures={row.balance !== undefined ? <Money value={row.balance} tone="neutral" /> : null}
                actions={restoreButton(group.rows, 'Stop ignoring')}
              />,
            ]
          }
          const expanded = open.has(group.institution)
          const Chevron = expanded ? ChevronDown : ChevronRight
          return [
            <ListRow
              key={`group:${group.institution}`}
              title={
                <>
                  <Chevron size={14} aria-hidden="true" className="muted" /> {institution}
                </>
              }
              sub={plural(group.rows.length, 'account')}
              onSelect={() => toggle(group.institution)}
              expanded={expanded}
              actions={restoreButton(group.rows, 'Stop ignoring all')}
            />,
            ...(expanded
              ? group.rows.map((row) => (
                  <ListRow
                    key={row.key}
                    className="list-row--member"
                    title={row.name}
                    sub={details(row.details)}
                    figures={row.balance !== undefined ? <Money value={row.balance} tone="neutral" /> : null}
                    actions={restoreButton([row], 'Stop ignoring')}
                  />
                ))
              : []),
          ]
        })}
      </List>
    </div>
  )
}

function details(parts: readonly (string | null)[]): string | undefined {
  const shown = parts.filter((part): part is string => Boolean(part))
  return shown.length > 0 ? shown.join(' · ') : undefined
}
