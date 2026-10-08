import { useQueryClient } from '@tanstack/react-query'
import { use, useState, type ReactNode } from 'react'
import {
  Ban,
  Building2,
  ExternalLink,
  Landmark,
  Link2,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
  Unplug,
} from 'lucide-react'

import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { AuthContext } from '@/contexts/auth'
import { withoutSmallBalances } from '@/components/shell/accountTree'
import { NewAccountDialog } from '@/components/shell/NewAccountDialog'
import { SmallBalancesRow } from '@/components/shell/SmallBalancesRow'
import {
  Badge,
  Button,
  Callout,
  Card,
  ConfirmDialog,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTrigger,
  EmptyState,
  Field,
  Input,
  NameDialog,
  OverflowMenu,
  PageHeader,
  RowActions,
  SkeletonRows,
  Spinner,
  Table,
  Td,
  Th,
  Tooltip,
  useConfirm,
} from '@/components/ui'
import { maskedNumber } from '@/lib/accounts'
import {
  CONNECTIONS_KEY,
  freshnessOf,
  isSyncing,
  useClaimConnection,
  useConnections,
  useDeleteAccount,
  useDeleteConnection,
  useRenameConnection,
  useReplaceSetupToken,
  useSyncConnection,
  useUpdateAccountSettings,
  type Connection,
  type Freshness,
  type SyncSchedule,
} from '@/lib/clients/connections'
import { serverSettingsBody, useSaveServerSettings, useServerSettings } from '@/lib/clients/admin'
import { useAcceptHeldBalance } from '@/lib/clients/heldBalances'
import { ZERO_MONEY } from '@/lib/money'
import { accountTypeLabel } from '@/lib/accountTypes'
import { AssetValuationCard } from './AssetValuationCard'
import { RefreshButton } from './connector/ConnectorControls'
import { AccountDetailsDialog } from './AccountDetailsDialog'
import { useRepriceAccount } from './useRepriceAccount'
import { ImportFileCard } from './ImportFileCard'
import { IgnoredAccountsCard } from './IgnoredAccountsCard'
import { MatchAccountsCard } from './MatchAccountsCard'
import { useIgnoreAccounts } from '@/lib/clients/ignoredAccounts'
import { useAccounts } from '@/lib/transactions/queries'
import type { AccountWithBalances } from '@/lib/transactions/types'
import { AskMenuItem } from '@/components/assistant/AskMenuItem'
import { accountSubject } from '@/lib/assistant/subjects'
import { asSentence, formatTimestamp, timeAgo } from '@/lib/format'
import { SyncProgress } from '@/components/SyncProgress'
import { syncSummary } from '@/components/syncText'

/** Where every connection problem this screen reports is fixed. */
const BRIDGE_URL = 'https://bridge.simplefin.org/'

function BridgeLink({ children = 'Open SimpleFIN Bridge' }: { children?: ReactNode }) {
  return (
    <Button size="sm" asChild>
      <a href={BRIDGE_URL} target="_blank" rel="noreferrer noopener">
        <ExternalLink size={14} aria-hidden="true" /> {children}
      </a>
    </Button>
  )
}

/**
 * An account whose sync reported nothing after a stable figure, so the last
 * balance is held rather than letting a stray zero reach net worth. Confirming
 * the zero applies it; until then the hold stands, and a later real balance
 * clears it on its own.
 */
export function HeldBalanceAlert({
  account,
}: {
  account: AccountWithBalances
}) {
  const accept = useConfirm(useAcceptHeldBalance())
  const moneyText = useMoneyText()

  const reported = moneyText(account.withheld_balance ?? ZERO_MONEY)
  const held =
    account.provider_balance != null ? moneyText(account.provider_balance) : 'its last balance'

  return (
    <Callout
      tone="warning"
      title={`${account.name} now reports ${reported}`}
      actions={
        <>
          <Button
            variant="primary"
            size="sm"
            disabled={accept.dialog.pending}
            onClick={() => accept.ask(account.id)}
          >
            Confirm
          </Button>
          <ConfirmDialog
            {...accept.dialog}
            title={`Confirm ${account.name} really holds ${reported}?`}
            description="Applies the reported balance and stops flagging zeros on this account."
            confirmLabel="Confirm"
          />
        </>
      }
    >
      {account.withheld_balance_reason ||
        `Held ${held} for weeks, now reports ${reported}. Showing ${held} until you confirm.`}
    </Callout>
  )
}

/**
 * Accounts, grouped by the connection that syncs them.
 *
 * A refused Access URL shows a reconnect prompt (a fresh setup token is the
 * only fix); a bank needing reauthorization behind a working connection shows
 * a warning under that connection. The freshness chip reads the last
 * *successful* sync, not the last attempt. The setup token is write-only.
 */
export function AccountsSettings() {
  const connections = useConnections()
  const accounts = useAccounts()

  const claim = useClaimConnection()
  const enabled = connections.data?.simplefin_enabled ?? false
  const rows = accounts.data ?? []
  const unlinked = rows.filter((account) => account.connection_id === null)
  const [addingByHand, setAddingByHand] = useState(false)
  const admin = use(AuthContext)?.user?.is_superuser ?? false

  return (
    <>
      <PageHeader
        title="Accounts"
        actions={
          enabled ? (
            <>
              <BridgeLink />
              <SetupTokenDialog
                title="Connect with SimpleFIN"
                submitLabel="Connect"
                pending={claim.isPending}
                onSubmit={(setupToken, name) =>
                  claim.mutateAsync({ setupToken, name: name || undefined })
                }
                trigger={
                  <Button variant="primary" size="sm">
                    <Plus size={13} /> New connection
                  </Button>
                }
              />
            </>
          ) : null
        }
      />

      <Card
        id="connections"
        title="Connections"
        subtitle="One SimpleFIN connection covers every bank linked at the Bridge."
      >
        {connections.isPending ? <SkeletonRows rows={2} /> : null}
        {connections.isSuccess && !enabled ? (
          <Callout>
            {admin ? (
              <TurnOnSimpleFin />
            ) : (
              'SimpleFIN is off on this server. Ask whoever runs Agentifi to turn it on.'
            )}
          </Callout>
        ) : null}
        {enabled && connections.isSuccess && connections.data.connections.length === 0 ? (
          <EmptyState
            icon={<Landmark size={20} />}
            title="No connections yet"
            body="Paste a setup token from bridge.simplefin.org."
          />
        ) : null}
        {enabled && connections.isSuccess && connections.data.connections.length > 0 ? (
          <ScheduleNote schedule={connections.data.schedule} />
        ) : null}
      </Card>

      {(connections.data?.connections ?? []).map((connection) =>
        connection.status === 'pending_link' ? (
          <MatchAccountsCard key={connection.id} connection={connection} />
        ) : (
          <ConnectionCard
            key={connection.id}
            connection={connection}
            accounts={rows.filter((account) => account.connection_id === connection.id)}
          />
        ),
      )}

      <ImportFileCard />

      <AssetValuationCard />

      <Card
        title="Manual accounts"
        subtitle="Kept by hand, or detached from a connection. Nothing syncs these."
        actions={<Button size="sm" onClick={() => setAddingByHand(true)}>Add an account by hand</Button>}
        flush={unlinked.length > 0}
      >
        {accounts.isPending ? <SkeletonRows rows={3} /> : null}
        {accounts.isSuccess && unlinked.length === 0 ? (
          <EmptyState compact title="Every account you have is connected." />
        ) : null}
        {unlinked.length > 0 ? (
          <AccountTable accounts={unlinked} />
        ) : null}
      </Card>

      <IgnoredAccountsCard connections={connections.data?.connections ?? []} />

      <NewAccountDialog open={addingByHand} onOpenChange={setAddingByHand} />
    </>
  )
}

/**
 * SimpleFIN ships off. An administrator turns it on here, through the same
 * server setting Server admin saves, unless the environment pins it.
 */
function TurnOnSimpleFin() {
  const settings = useServerSettings()
  const save = useSaveServerSettings()
  const client = useQueryClient()
  const setting = settings.data?.settings.find((one) => one.key === SIMPLEFIN_SETTING)
  const intro = (
    <>
      SimpleFIN is off on this server. Turn it on to connect your banks and import their
      transactions automatically; without it, accounts are kept by hand or from statement files.
    </>
  )
  if (setting?.source === 'environment') {
    return (
      <>
        {intro} It is switched off in the server&rsquo;s environment, so set{' '}
        <code>{SIMPLEFIN_SETTING}=true</code> there (or in the <code>.env</code>) and restart.
      </>
    )
  }
  return (
    <div className="stack stack--3">
      <span>{intro}</span>
      <span>
        <Button
          size="sm"
          variant="primary"
          disabled={settings.data === undefined || save.isPending}
          onClick={() => {
            if (settings.data === undefined) return
            save.mutate(
              serverSettingsBody(settings.data.settings, { [SIMPLEFIN_SETTING]: 'true' }),
              { onSuccess: () => void client.invalidateQueries({ queryKey: CONNECTIONS_KEY }) },
            )
          }}
        >
          {save.isPending ? 'Turning on…' : 'Turn on SimpleFIN'}
        </Button>
      </span>
    </div>
  )
}

const SIMPLEFIN_SETTING = 'SIMPLEFIN_ENABLED'

function ConnectionCard({
  connection,
  accounts,
}: {
  connection: Connection
  accounts: AccountWithBalances[]
}) {
  const sync = useSyncConnection()
  const rename = useRenameConnection()
  const remove = useConfirm(useDeleteConnection())
  const reconnect = useReplaceSetupToken()
  const [matching, setMatching] = useState(false)
  const running = isSyncing(connection)
  const run = connection.sync
  const runSync = () => sync.mutate(connection.id)

  // Matching is a whole screen's worth of question, so it replaces the card
  // rather than opening inside it.
  if (matching) {
    return (
      <MatchAccountsCard
        connection={connection}
        onDone={() => setMatching(false)}
      />
    )
  }

  const troubled =
    connection.needs_setup_token ||
    connection.status === 'rate_limited' ||
    connection.bank_warnings.length > 0 ||
    accounts.some((account) => account.withheld_balance_at !== null)

  return (
    <Card
      className={troubled ? 'card--alert' : undefined}
      title={
        <>
          <Building2 size={16} aria-hidden="true" /> {connection.name}
        </>
      }
      subtitle={`Last successful sync ${timeAgo(connection.last_successful_sync_at, 'long')}${
        run?.state === 'succeeded' ? ` · ${syncSummary(run)}` : ''
      }`}
      actions={
        <>
          <FreshnessChip freshness={freshnessOf(connection)} />
          <RefreshButton
            busy={sync.isPending || running}
            label="Sync now"
            busyLabel="Syncing…"
            onClick={runSync}
          />
          <ConnectionMenu
            connection={connection}
            onRename={(name) => rename.mutateAsync({ id: connection.id, name })}
            onRemove={() => remove.ask(connection.id)}
            onReconnect={(setupToken) =>
              reconnect.mutateAsync({ id: connection.id, setupToken })
            }
            onMatch={() => setMatching(true)}
          />
        </>
      }
      flush={accounts.length > 0}
    >
      {running && run !== null ? <SyncProgress run={run} /> : null}

      {run?.state === 'failed' && connection.status === 'active' ? (
        <Callout
          tone="warning"
          title="The last sync did not finish"
          actions={
            <Button size="sm" disabled={sync.isPending} onClick={runSync}>
              Try again
            </Button>
          }
        >
          {asSentence(run.message ?? 'It stopped before importing everything.')} What it imported is
          kept.
        </Callout>
      ) : null}

      {connection.needs_setup_token ? (
        <Callout
          tone="warning"
          title="This connection needs a fresh setup token"
          actions={
            <>
              <BridgeLink>Bridge</BridgeLink>
              <SetupTokenDialog
                title="Reconnect with a fresh token"
                submitLabel="Reconnect"
                pending={reconnect.isPending}
                withName={false}
                onSubmit={(setupToken) =>
                  reconnect.mutateAsync({ id: connection.id, setupToken })
                }
                trigger={
                  <Button variant="primary" size="sm">
                    Reconnect
                  </Button>
                }
              />
            </>
          }
        >
          {connection.status_detail ??
            'SimpleFIN refused the stored credential. Generate a new setup token at the Bridge.'}{' '}
          Accounts and history are kept.
        </Callout>
      ) : null}

      {connection.status === 'rate_limited' ? (
        <Callout tone="warning" title="SimpleFIN is rate limiting this connection">
          Syncing resumes on its own
          {connection.retry_not_before
            ? ` after ${formatTimestamp(connection.retry_not_before)}`
            : ''}
          .
        </Callout>
      ) : null}

      {connection.bank_warnings.map((warning) => (
        <Callout
          tone="warning"
          key={`${warning.institution}-${warning.at}`}
          title={`${warning.institution || 'One institution'} needs reauthorizing at the Bridge`}
          actions={<BridgeLink>Bridge</BridgeLink>}
        >
          {warning.message.replace(/\.\s*$/, '')}. Other banks still sync.
        </Callout>
      ))}

      {accounts
        .filter((account) => account.withheld_balance_at !== null)
        .map((account) => (
          <HeldBalanceAlert key={account.id} account={account} />
        ))}

      {accounts.length === 0 && connection.ignored.length === 0 ? (
        <Callout
          tone="warning"
          title="This connection reaches no accounts yet"
          actions={<BridgeLink>Bridge</BridgeLink>}
        >
          Sync it, or check what you shared at the Bridge. Wrong names? Match them by hand from
          the menu.
        </Callout>
      ) : null}
      {accounts.length > 0 ? (
        <AccountTable accounts={accounts} />
      ) : null}

      <ConfirmDialog
        {...remove.dialog}
        title={`Remove ${connection.name}?`}
        description="Syncing stops. Accounts and transactions stay, as manual accounts."
        confirmLabel="Remove connection"
      >
        <p className="hint">Nothing is deleted.</p>
      </ConfirmDialog>
    </Card>
  )
}

const FRESHNESS: Record<Freshness, { label: string; tone: 'neutral' | 'accent' | 'warning' }> = {
  fresh: { label: 'Up to date', tone: 'accent' },
  stale: { label: 'Over a day old', tone: 'warning' },
  never: { label: 'Never synced', tone: 'neutral' },
  failing: { label: 'Needs attention', tone: 'warning' },
  parked: { label: 'Rate limited', tone: 'warning' },
}

/**
 * When the server syncs on its own. The zone is named because the window is
 * the server's local time.
 */
function ScheduleNote({ schedule }: { schedule: SyncSchedule }) {
  if (!schedule.enabled) {
    return (
      <p className="hint">
        Automatic sync is off; use Sync now. Set <code>SYNC_ENABLED=true</code> to turn it on.
      </p>
    )
  }
  return (
    <p className="hint">
      Syncs once a day at {schedule.at} ({schedule.time_zone})
      {schedule.next_run_at === null ? '' : `, next ${timeAgo(schedule.next_run_at, 'long')}`}
    </p>
  )
}

function FreshnessChip({ freshness }: { freshness: Freshness }) {
  const { label, tone } = FRESHNESS[freshness]
  return <Badge tone={tone}>{label}</Badge>
}

function ConnectionMenu({
  connection,
  onRename,
  onRemove,
  onReconnect,
  onMatch,
}: {
  connection: Connection
  onRename: (name: string) => Promise<unknown>
  /** Ask to remove the connection. */
  onRemove: () => void
  onReconnect: (setupToken: string) => Promise<unknown>
  /** Re-open the match screen. Matching is not a one-time act: a feed that
   *  built its own duplicate of an account you already had is re-pointed here. */
  onMatch: () => void
}) {
  const [renaming, setRenaming] = useState(false)
  const [reconnecting, setReconnecting] = useState(false)

  return (
    <>
      <OverflowMenu
        label={`Actions for ${connection.name}`}
        actions={[
          { label: 'Match accounts…', icon: <Link2 size={14} />, onSelect: onMatch },
          {
            label: 'Rename connection…',
            icon: <Pencil size={14} />,
            onSelect: () => setRenaming(true),
          },
          {
            label: 'Replace the setup token…',
            icon: <Unplug size={14} />,
            onSelect: () => setReconnecting(true),
          },
          { label: 'Open SimpleFIN Bridge', icon: <ExternalLink size={14} />, href: BRIDGE_URL },
          {
            label: 'Remove connection',
            icon: <Trash2 size={14} />,
            danger: true,
            onSelect: onRemove,
          },
        ]}
      />

      <NameDialog
        open={renaming}
        onOpenChange={setRenaming}
        title="Rename connection"
        description="Changes nothing at the Bridge."
        initial={connection.name}
        onSubmit={onRename}
      />

      <SetupTokenDialog
        title="Replace the setup token"
        submitLabel="Reconnect"
        withName={false}
        open={reconnecting}
        onOpenChange={setReconnecting}
        onSubmit={(setupToken) => onReconnect(setupToken)}
      />
    </>
  )
}

/**
 * The paste-a-token form. The single-use warning is shown up front, because a
 * refused token has already been spent.
 */
function SetupTokenDialog({
  title,
  submitLabel,
  onSubmit,
  trigger,
  withName = true,
  pending = false,
  open,
  onOpenChange,
}: {
  title: string
  submitLabel: string
  onSubmit: (setupToken: string, name: string) => Promise<unknown>
  trigger?: ReactNode
  withName?: boolean
  pending?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const [internalOpen, setInternalOpen] = useState(false)
  const [token, setToken] = useState('')
  const [name, setName] = useState('')

  const isOpen = open ?? internalOpen
  const setOpen = (next: boolean) => {
    // The token never outlives the dialog. It is single-use and worth nothing
    // once spent, and there is no reason for it to sit in memory afterwards.
    if (!next) setToken('')
    onOpenChange?.(next)
    if (open === undefined) setInternalOpen(next)
  }

  const submit = () => {
    const pasted = token.trim()
    if (!pasted) return
    void onSubmit(pasted, name.trim()).then(
      () => setOpen(false),
      // The failure is already a toast; the field is cleared because a refused
      // token is spent.
      () => setToken(''),
    )
  }

  return (
    <Dialog open={isOpen} onOpenChange={setOpen}>
      {trigger ? <DialogTrigger asChild>{trigger}</DialogTrigger> : null}
      <DialogContent
        title={title}
        description="Paste a setup token from bridge.simplefin.org."
        footer={
          <DialogActions>
            <Button variant="primary" onClick={submit} disabled={pending || token.trim() === ''}>
              {pending ? 'Connecting…' : submitLabel}
            </Button>
          </DialogActions>
        }
      >
        <div className="settings__form">
          <Field
            label="Setup token"
            hint="Single-use. A claimed token needs a fresh one from the Bridge."
          >
            <Input
              value={token}
              onChange={(event) => setToken(event.target.value)}
              placeholder="aHR0cHM6Ly9icmlkZ2Uuc2ltcGxlZmluLm9yZy8…"
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          {withName ? (
            <Field label="Name" hint="Optional.">
              <Input
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder="SimpleFIN"
              />
            </Field>
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  )
}

function AccountTable({
  accounts,
}: {
  accounts: AccountWithBalances[]
}) {
  // The drawer's rule: an account the server hid for holding next to nothing
  // is left out behind a line that says so, never dropped.
  const [revealed, setRevealed] = useState(false)
  const { shown, hidden } = withoutSmallBalances(
    accounts,
    (account) => account.hidden_small_balance === true,
    revealed,
  )

  return (
    <Table density="sm">
      <thead>
        <tr>
          <Th>Account</Th>
          <Th>Type</Th>
          <Th numeric>Balance</Th>
          <Th aria-label="Actions" />
        </tr>
      </thead>
      <tbody>
        {shown.map((account) => (
          <tr key={account.id}>
            <Td>
              <span className="account-name">
                <span className="cell__clip" title={accountLabel(account)}>
                  {account.name}
                  {account.masked_number ? (
                    <span className="hint"> {maskedNumber(account.masked_number)}</span>
                  ) : null}
                </span>
                <AccountFlags account={account} />
              </span>
            </Td>
            <Td>
              <span className="cell__clip" title={accountTypeLabel(account.type)}>
                {accountTypeLabel(account.type)}
              </span>
            </Td>
            <Td numeric>
              <Money value={account.balances.balance} tone="neutral" />
            </Td>
            <Td numeric>
              <RowActions>
                <AccountMenu account={account} />
              </RowActions>
            </Td>
          </tr>
        ))}
        <SmallBalancesRow
          colSpan={4}
          hidden={hidden}
          revealed={revealed}
          onToggle={() => setRevealed(!revealed)}
        />
      </tbody>
    </Table>
  )
}

function accountLabel(account: AccountWithBalances): string {
  const mask = maskedNumber(account.masked_number)
  return mask ? `${account.name} ${mask}` : account.name
}

/** The flags worth seeing without opening the menu. */
function AccountFlags({ account }: { account: AccountWithBalances }) {
  const held = account.withheld_balance_at !== null
  const flags: string[] = []
  if (account.is_closed) flags.push('Closed')
  if (account.excluded_from_reports) flags.push('Not in reports')
  if (account.excluded_from_spending_plan) flags.push('Not in the plan')
  if (!account.include_in_net_worth) flags.push('Not in net worth')
  if (account.hidden_small_balance) flags.push('Small balance')
  if (!held && flags.length === 0) return null

  return (
    <span className="chips chips--tight account-name__flags">
      {held ? <Badge tone="warning">Balance held</Badge> : null}
      {/* One badge whatever the count, so a flagged account's row is no taller
          than the rest; the tooltip names every flag. */}
      {flags.length === 1 ? <Badge>{flags[0]}</Badge> : null}
      {flags.length > 1 ? (
        <Tooltip side="top" label={flags.join(', ')}>
          <Badge>
            {flags[0]} +{flags.length - 1}
          </Badge>
        </Tooltip>
      ) : null}
    </span>
  )
}

export function AccountMenu({
  account,
}: {
  account: AccountWithBalances
}) {
  const update = useUpdateAccountSettings()
  const remove = useConfirm(useDeleteAccount())
  const ignore = useConfirm(useIgnoreAccounts(), {
    variables: (id: string) => [id],
  })
  const [editing, setEditing] = useState(false)
  const reprice = useRepriceAccount(account)

  const patch = (changes: Parameters<typeof update.mutate>[0]['patch']) =>
    update.mutate({ id: account.id, patch: changes })

  return (
    <>
      <OverflowMenu
        label={`Actions for ${account.name}`}
        actions={[
          <AskMenuItem
            subject={() =>
              accountSubject({
                id: account.id,
                name: account.name,
                balance: account.balances.balance,
                kind: account.kind,
              })
            }
          />,
        ]}
        sections={[
          {
            entries: [
              { label: 'Edit account', icon: <Pencil size={14} />, onSelect: () => setEditing(true) },
              reprice.available && {
                label: reprice.pending ? 'Re-pricing…' : 'Re-price value',
                icon: reprice.pending ? <Spinner /> : <RefreshCw size={14} />,
                disabled: reprice.pending,
                onSelect: reprice.reprice,
              },
              { label: 'Ignore account', icon: <Ban size={14} />, onSelect: () => ignore.ask(account.id) },
              {
                label: 'Delete account',
                icon: <Trash2 size={14} />,
                danger: true,
                onSelect: () => remove.ask(account.id),
              },
            ],
          },
          {
            label: 'Exclude from',
            // Four separate questions, four separate flags. Setting one must not
            // move another — see the two exclusion columns in the schema.
            entries: [
              {
                label: 'Reports',
                checked: account.excluded_from_reports,
                onCheckedChange: (checked) => patch({ excluded_from_reports: checked }),
              },
              {
                label: 'Spending Plan',
                checked: account.excluded_from_spending_plan,
                onCheckedChange: (checked) => patch({ excluded_from_spending_plan: checked }),
              },
              {
                label: 'The account bar',
                checked: account.excluded_from_account_bar,
                onCheckedChange: (checked) => patch({ excluded_from_account_bar: checked }),
              },
              {
                label: 'Net worth',
                checked: !account.include_in_net_worth,
                onCheckedChange: (checked) => patch({ include_in_net_worth: !checked }),
              },
            ],
          },
        ]}
      />

      <AccountDetailsDialog account={account} open={editing} onOpenChange={setEditing} />

      <ConfirmDialog
        {...ignore.dialog}
        title={`Ignore ${account.name}?`}
        description="Leaves every list, total, report and plan, and stops syncing. History is kept."
        confirmLabel="Ignore account"
      >
        <p className="hint">
          Transfers to it stay transfers. Restore it from Ignored accounts below.
        </p>
      </ConfirmDialog>

      <ConfirmDialog
        {...remove.dialog}
        title={`Delete ${account.name}?`}
        description="Removed from every list and total. Its transactions stay on the server."
        confirmLabel="Delete account"
      >
        <p className="hint">
          Done using it? Close it from Edit account instead to keep its history.
        </p>
      </ConfirmDialog>
    </>
  )
}

