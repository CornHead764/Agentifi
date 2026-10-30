import { Plus } from 'lucide-react'
import { useEffect, useEffectEvent, useMemo, useState } from 'react'
import { Navigate, useLocation, useSearchParams } from 'react-router-dom'

import { QueryBoundary } from '@/components/QueryBoundary'
import { useSignIns } from '@/components/signin/signInTasks-context'
import {
  Button,
  Card,
  ConfirmDialog,
  EmptyState,
  PageHeader,
  useConfirm,
} from '@/components/ui'
import { connectionTitle, sortedByTitle } from '@/lib/billers'
import {
  billProviderOf,
  useAllBillSubaccounts,
  useBillAgent,
  useBillChallenges,
  useBillConnections,
  useDeleteBillConnection,
  type BillConnection,
  type BillSubaccount,
} from '@/lib/clients/bills'
import { plural } from '@/lib/format'

import { ChallengeDialog } from './bills/ChallengeDialog'
import { ConnectionCard } from './bills/ConnectionCard'
import { ConnectionDialog } from './bills/ConnectionDialog'
import { useProviderFollowUp } from './bills/providerFollowUp'
import { ConnectorList, EngineNotice } from './connector/ConnectorRow'
import { billEngine } from './connector/engine'

/**
 * Bill providers. Subaccounts, the agent's answer and the waiting code
 * requests are read once for the whole space rather than per connection.
 *
 * `?challenge=<id>` (the push notification's URL) opens the challenge dialog,
 * and is cleared on close so a reload does not reopen an answered request.
 */
export function BillsSettings() {
  // A link to #mail-rules or #mailbox on this page redirects to the same
  // hash on Email.
  const { hash } = useLocation()
  if (hash === '#mail-rules' || hash === '#mailbox') {
    return <Navigate to={`/settings/email${hash}`} replace />
  }
  return <BillProviders />
}

function BillProviders() {
  const connections = useBillConnections()
  const subaccounts = useAllBillSubaccounts()
  const agent = useBillAgent()
  const challenges = useBillChallenges()
  const remove = useConfirm(useDeleteBillConnection(), {
    variables: (connection: BillConnection) => connection.id,
  })

  const [params, setParams] = useSearchParams()
  const openChallenge = params.get('challenge')
  const dropParam = (name: string) => {
    const next = new URLSearchParams(params)
    next.delete(name)
    setParams(next, { replace: true })
  }

  const [adding, setAddingState] = useState(() => params.has('add'))
  const setAdding = (open: boolean) => {
    setAddingState(open)
    if (!open && params.has('add')) dropParam('add')
  }
  const followUp = useProviderFollowUp()
  const [editing, setEditing] = useState<BillConnection | null>(null)
  // A sign-in is the shell's rather than this page's, so leaving the page
  // does not give it up.
  const signIns = useSignIns()
  const connect = (connection: BillConnection, retry = false) =>
    signIns.open({
      kind: 'bill',
      connection,
      provider: billProviderOf(agent.data, connection.biller),
      retry,
    })

  // `?sign-in=<connection>` is what the re-sign-in alert carries. Opened once,
  // then the parameter goes.
  const signInFor = params.get('sign-in')
  const signingIn =
    signInFor === null ? null : (connections.data?.find((one) => one.id === signInFor) ?? null)
  const openSignIn = useEffectEvent((connection: BillConnection) => {
    connect(connection)
    dropParam('sign-in')
  })
  useEffect(() => {
    if (signingIn !== null && agent.isFetched) openSignIn(signingIn)
  }, [signingIn, agent.isFetched])

  const showChallenge = (challengeId: string) => {
    const next = new URLSearchParams(params)
    next.set('challenge', challengeId)
    setParams(next, { replace: true })
  }
  const closeChallenge = () => {
    const next = new URLSearchParams(params)
    next.delete('challenge')
    setParams(next, { replace: true })
  }

  const byConnection = useMemo(() => {
    const grouped = new Map<string, BillSubaccount[]>()
    for (const one of subaccounts.data ?? []) {
      const held = grouped.get(one.connection_id)
      if (held) held.push(one)
      else grouped.set(one.connection_id, [one])
    }
    return grouped
  }, [subaccounts.data])

  // Read off the waiting list, only to name the dialog; it reads the challenge
  // itself.
  const challenged =
    (openChallenge === null
      ? null
      : connections.data?.find(
          (one) =>
            one.id ===
            (challenges.data ?? []).find((challenge) => challenge.id === openChallenge)
              ?.connection_id,
        )) ?? null

  return (
    <>
      <PageHeader
        title="Bill providers"
        actions={
          <Button variant="primary" size="sm" onClick={() => setAdding(true)}>
            <Plus size={13} aria-hidden="true" /> Add provider
          </Button>
        }
      />

      <Card>
        <EngineNotice engine={billEngine(agent.data)} />
        <QueryBoundary
          query={connections}
          rows={3}
          empty={(rows) =>
            rows.length === 0 ? (
              <EmptyState
                compact
                title="No bill providers yet. Add provider lists the built-in ones."
              />
            ) : undefined
          }
        >
          {(rows) => (
            <ConnectorList>
              {sortedByTitle(rows).map((connection) => (
                <ConnectionCard
                  key={connection.id}
                  connection={connection}
                  subaccounts={byConnection.get(connection.id) ?? []}
                  agent={agent.data}
                  provider={billProviderOf(agent.data, connection.biller)}
                  challenges={challenges.data ?? []}
                  onEdit={() => setEditing(connection)}
                  onDelete={() => remove.ask(connection)}
                  onConnect={() => connect(connection)}
                  onRetry={() => connect(connection, true)}
                  onWriteMailRule={() => followUp.writeMailRule(connection)}
                  onChallenge={showChallenge}
                />
              ))}
            </ConnectorList>
          )}
        </QueryBoundary>
      </Card>


      {adding ? (
        <ConnectionDialog
          connection={null}
          onClose={() => setAdding(false)}
          onCreated={followUp.next}
        />
      ) : null}
      {followUp.dialog}
      {editing ? (
        <ConnectionDialog
          connection={editing}
          onClose={() => setEditing(null)}
        />
      ) : null}
      {openChallenge !== null ? (
        <ChallengeDialog
          challengeId={openChallenge}
          connection={challenged}
          onClose={closeChallenge}
          onSignInAgain={() => {
            closeChallenge()
            if (challenged) connect(challenged)
          }}
        />
      ) : null}

      <ConfirmDialog
        {...remove.dialog}
        title={remove.target ? `Remove ${connectionTitle(remove.target)}?` : 'Remove this provider?'}
        description={
          remove.target
            ? `${describeLoss(byConnection.get(remove.target.id)?.length ?? 0)} Linked reminders keep their dates and amounts; no transaction is touched.`
            : undefined
        }
        confirmLabel="Remove provider"
      />
    </>
  )
}

/** What goes with a connection, said as a count rather than as "related data". */
function describeLoss(subaccounts: number): string {
  if (subaccounts === 0) return 'Nothing is billed under this login yet.'
  return `Deletes its ${plural(subaccounts, 'account')} and their bills.`
}
