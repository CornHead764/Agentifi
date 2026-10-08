import { ArrowRight, Ban, EyeOff, Landmark } from 'lucide-react'
import { useState } from 'react'

import { Money } from '@/components/Money'
import {
  Button,
  Callout,
  Card,
  Checkbox,
  EmptyState,
  MoneyInput,
  OptionSelect,
  SkeletonRows,
  Table,
  Td,
  Th,
} from '@/components/ui'
import { accountDisplayName, isUnderBalance, maskedNumber } from '@/lib/accounts'
import {
  useFinishLinking,
  useLinkCandidates,
  useRestoreRemoteAccount,
  type Connection,
  type LinkTarget,
  type RemoteAccount,
} from '@/lib/clients/connections'
import { accountTypeLabel } from '@/lib/accountTypes'
import { plural } from '@/lib/format'
import { parseAmountInput } from '@/lib/money'

import { IgnoredList } from './IgnoredAccountsCard'
import { remoteIgnoredRows } from './ignoredRows'
import { IGNORE, linkChoice, matchNote, NEW_ACCOUNT, offered, resolveChoices } from './matchAccounts'

/**
 * The match screen. Accounts imported from Simplifi have no provider id, so a
 * claimed connection imports nothing until each pairing is confirmed; letting
 * it create its own accounts would duplicate the history. A likely match is
 * chosen in advance; every other account starts as a new one. The choices stay
 * on the screen until Finish writes them all at once and starts the sync.
 */
export function MatchAccountsCard({
  connection,
  onDone,
}: {
  connection: Connection
  /** Called once the pairings are released; absent while the connection awaits its first link. */
  onDone?: () => void
}) {
  // Enabled unconditionally: re-matching is the only way to point a feed at an
  // account it duplicated rather than adopted.
  const candidates = useLinkCandidates(connection.id, true)
  const restore = useRestoreRemoteAccount()
  const finish = useFinishLinking()
  // What the household picked on this screen, by bank account.
  const [chosen, setChosen] = useState<Record<string, string>>({})
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [under, setUnder] = useState('1.00')
  const busy = finish.isPending

  const remote = candidates.data?.remote ?? []
  const local = candidates.data?.local ?? []
  const values = resolveChoices(remote, local, chosen)
  const valueOf = (one: RemoteAccount) => values[one.external_id]
  // An account set not to import leaves the table for the Not imported list.
  const importing = remote.filter((one) => valueOf(one) !== IGNORE)
  const skipping = remote.filter((one) => valueOf(one) === IGNORE)
  const picked = importing.filter((one) => selected.has(one.external_id))
  const threshold = parseAmountInput(under)
  const refused = candidates.data?.ignored ?? []

  const choose = (one: RemoteAccount, value: string) => {
    setChosen((current) => ({ ...current, [one.external_id]: value }))
    if (value === IGNORE) {
      setSelected((current) => {
        const next = new Set(current)
        next.delete(one.external_id)
        return next
      })
    }
  }
  const importAfterAll = (one: RemoteAccount) =>
    setChosen((current) => {
      const next = { ...current }
      delete next[one.external_id]
      return next
    })

  const ignoreSelected = () => {
    setChosen((current) => {
      const next = { ...current }
      for (const one of picked) next[one.external_id] = IGNORE
      return next
    })
    setSelected(new Set())
  }

  const finishAndSync = () =>
    finish.mutate(
      { id: connection.id, choices: remote.map((one) => linkChoice(one.external_id, valueOf(one))) },
      { onSuccess: () => onDone?.() },
    )

  const everySelected = importing.length > 0 && picked.length === importing.length

  return (
    <Card
      title={
        <>
          <Landmark size={16} aria-hidden="true" /> Match {connection.name} to your accounts
        </>
      }
      subtitle="Nothing imports until you finish. A paired account keeps its name and history."
      actions={
        <>
          {onDone ? (
            <Button size="sm" disabled={busy} onClick={onDone}>
              Cancel
            </Button>
          ) : null}
          <Button
            variant="primary"
            size="sm"
            disabled={busy || candidates.isPending}
            onClick={finishAndSync}
          >
            {busy ? 'Finishing…' : 'Finish and sync'}
          </Button>
        </>
      }
      flush={remote.length > 0}
    >
      {candidates.isPending ? <SkeletonRows rows={3} /> : null}
      {candidates.isError ? (
        <EmptyState
          title="Could not reach the Bridge"
          body="The connection is saved. Try again shortly."
        />
      ) : null}
      {candidates.isSuccess &&
      candidates.data.remote.length === 0 &&
      candidates.data.ignored.length === 0 ? (
        <EmptyState
          title="This connection reaches no accounts"
          body="Nothing was shared at the Bridge. Add an account there, or remove this connection."
        />
      ) : null}
      {candidates.isSuccess && importing.length === 0 && refused.length + skipping.length > 0 ? (
        <Callout>
          Every account this connection reaches is being ignored. Finishing imports nothing.
        </Callout>
      ) : null}

      {importing.length > 0 ? (
        <>
          <div className="toolbar match-bulk">
            <span className="toolbar__note">Select balances under</span>
            <span className="match-bulk__amount">
              <MoneyInput
                size="sm"
                value={under}
                onChange={(event) => setUnder(event.target.value)}
                aria-label="Small balance threshold"
              />
            </span>
            <Button
              size="sm"
              disabled={threshold === null}
              onClick={() => {
                if (threshold === null) return
                setSelected(
                  new Set(
                    importing
                      .filter((one) => isUnderBalance(one.balance, threshold.cents))
                      .map((one) => one.external_id),
                  ),
                )
              }}
            >
              Select
            </Button>
            <span className="toolbar__spacer" />
            <Button size="sm" disabled={picked.length === 0 || busy} onClick={ignoreSelected}>
              <Ban size={13} aria-hidden="true" />{' '}
              {picked.length === 0 ? 'Do not import' : `Do not import ${plural(picked.length, 'account')}`}
            </Button>
          </div>
          <Table lines={2}>
            <thead>
              <tr>
                <Th className="match__select">
                  <Checkbox
                    aria-label={everySelected ? 'Clear the selection' : 'Select every account'}
                    checked={everySelected ? true : picked.length > 0 ? 'indeterminate' : false}
                    onCheckedChange={() =>
                      setSelected(
                        everySelected ? new Set() : new Set(importing.map((one) => one.external_id)),
                      )
                    }
                  />
                </Th>
                <Th>At the bank</Th>
                <Th numeric>Balance</Th>
                <Th>
                  <span className="visually-hidden">pairs with</span>
                </Th>
                <Th className="match__pair">Your account</Th>
              </tr>
            </thead>
            <tbody>
              {importing.map((one) => {
                const name = accountDisplayName(one.name)
                const about = [one.institution, accountTypeLabel(one.kind), maskedNumber(one.masked_number)]
                  .filter(Boolean)
                  .join(' · ')
                const value = valueOf(one)
                const note = matchNote(one, local, chosen[one.external_id], value)
                return (
                  <tr key={one.external_id} data-selected={selected.has(one.external_id) || undefined}>
                    <Td className="match__select">
                      <Checkbox
                        aria-label={`Select ${name}`}
                        checked={selected.has(one.external_id)}
                        onCheckedChange={() =>
                          setSelected((current) => {
                            const next = new Set(current)
                            if (!next.delete(one.external_id)) next.add(one.external_id)
                            return next
                          })
                        }
                      />
                    </Td>
                    <Td>
                      <span className="cell__clip" title={name}>
                        {name}
                      </span>
                      <span className="cell__sub cell__clip" title={about}>
                        {about}
                      </span>
                    </Td>
                    <Td numeric>
                      <Money value={one.balance} tone="neutral" />
                    </Td>
                    <Td>
                      <ArrowRight size={14} aria-hidden="true" className="muted" />
                    </Td>
                    <Td className="match__pair">
                      <OptionSelect
                        value={value}
                        onValueChange={(next) => choose(one, next)}
                        disabled={busy}
                        size="sm"
                        aria-label={`Pair ${name}`}
                        options={[
                          { value: NEW_ACCOUNT, label: 'Create a new account' },
                          ...local
                            .filter((target) => offered(target, one, values))
                            .map((target) => ({ value: target.id, label: label(target, one) })),
                          { value: IGNORE, label: 'Do not import this account' },
                        ]}
                      />
                      <span className="cell__sub cell__clip" title={note}>
                        {note}
                      </span>
                    </Td>
                  </tr>
                )
              })}
            </tbody>
          </Table>
        </>
      ) : null}

      {candidates.isSuccess && refused.length + skipping.length > 0 ? (
        <IgnoredList
          title={
            <>
              <EyeOff size={13} aria-hidden="true" /> Not imported ({refused.length + skipping.length})
            </>
          }
          rows={[
            ...skipping.map((one) => ({
              key: `choice:${one.external_id}`,
              name: accountDisplayName(one.name),
              institution: one.institution,
              details: [maskedNumber(one.masked_number), 'Saved when you finish'],
              balance: one.balance,
              restore: async () => importAfterAll(one),
            })),
            ...remoteIgnoredRows(
              { id: connection.id, ignored: refused },
              (id, ignoredId) => restore.mutateAsync({ id, ignoredId }),
              null,
            ),
          ]}
        />
      ) : null}
    </Card>
  )
}

function label(target: LinkTarget, remote: RemoteAccount): string {
  const likely = remote.likely === target.id ? ' — likely match' : ''
  const masked = maskedNumber(target.masked_number)
  return `${target.name}${masked === null ? '' : ` ${masked}`}${likely}`
}
