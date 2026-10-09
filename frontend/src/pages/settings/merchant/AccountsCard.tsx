import {
  FileText,
  History,
  KeyRound,
  LogIn,
  Pencil,
  Plus,
  RefreshCw,
  ShieldCheck,
  Trash2,
  Unplug,
} from 'lucide-react'
import { useEffect, useEffectEvent, useState, type ReactNode } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

import { ConnectorFailureNote } from '@/components/ConnectorFailureNote'
import { connectorFailureToast } from '@/components/failure-screenshot'
import { Money } from '@/components/Money'
import { useSignIns } from '@/components/signin/signInTasks-context'
import {
  Button,
  Card,
  ConfirmDialog,
  EmptyState,
  NameDialog,
  type OverflowAction,
  SkeletonRows,
  Switch,
  useConfirm,
  useProgressToast,
  useToast,
} from '@/components/ui'
import {
  describeBackfill,
  describeMerchantPullFailure,
  describeSync,
  merchantFailureScreenshot,
  merchantPullFailureScreenshot,
  useCreateMerchantAccount,
  useDeleteMerchantAccount,
  useForgetMerchantPassword,
  useForgetMerchantSession,
  usePullMerchant,
  useUpdateMerchantAccount,
  type MerchantAccount,
  type MerchantAgentStatus,
} from '@/lib/clients/merchant'
import { capitalize, plural, timeAgo } from '@/lib/format'
import { MERCHANTS, type MerchantId } from '@/lib/merchants'

import {
  ConnectorFact,
  ConnectorList,
  ConnectorRow,
  EngineNotice,
} from '../connector/ConnectorRow'
import {
  ConnectorBadge,
  RefreshButton,
  SessionExpiredNote,
  SignInButton,
} from '../connector/ConnectorControls'
import { engineSignsIn, merchantEngine } from '../connector/engine'
import { ForgetSignInConfirm } from '../connector/ForgetSignInConfirm'
import { forgetPasswordLabel, signInLabel } from '../connector/signInNeed'
import { useForgetSignIn } from '../connector/useForgetSignIn'
import {
  accountActions,
  accountStopped,
  passwordFact,
  accountSignInNeed,
  signsInAs,
  type AccountMenuAction,
  type BackfillOffer,
} from './actions'
import { BackfillDialog } from './BackfillDialog'
import { PullHistory } from './PullHistory'
import { PullProgressLine } from './PullProgressLine'

export function AccountsCard({
  merchant,
  accounts,
  loading,
  agent,
}: {
  merchant: MerchantId
  accounts: MerchantAccount[]
  loading: boolean
  agent: MerchantAgentStatus | undefined
}) {
  const { name, noun, nounPlural, files } = MERCHANTS[merchant]
  const { show } = useToast()
  const progress = useProgressToast()
  const [editing, setEditing] = useState<MerchantAccount | null>(null)
  const [reaching, setReaching] = useState<MerchantAccount | null>(null)
  const [backfilling, setBackfilling] = useState<MerchantAccount | null>(null)
  // A sign-in is the shell's rather than this card's, so leaving the page
  // does not give it up.
  const signIns = useSignIns()
  const signIn = (account: MerchantAccount | null) =>
    signIns.open({ kind: 'merchant', merchant, account })
  const create = useCreateMerchantAccount(merchant)
  const remove = useDeleteMerchantAccount(merchant)
  const rename = useUpdateMerchantAccount(merchant)
  const removing = useConfirm(remove, {
    variables: (account: MerchantAccount) => account.id,
  })
  const forget = useForgetSignIn(useForgetMerchantSession(merchant))
  const forgetPassword = useConfirm(useForgetMerchantPassword(merchant), {
    variables: (account: MerchantAccount) => account.id,
  })
  const pull = usePullMerchant(merchant)
  const engine = merchantEngine(agent)
  const canSignIn = engineSignsIn(engine)
  const backfillOffer: BackfillOffer = {
    invoices: MERCHANTS[merchant].invoices,
    engine: agent?.configured ?? false,
  }

  // `?sign-in=<account>` is what the "needs you to sign in again" alert
  // carries: tapping it opens that account's sign-in, once.
  const [params, setParams] = useSearchParams()
  const signInFor = params.get('sign-in')
  const signingIn =
    signInFor === null ? null : (accounts.find((one) => one.id === signInFor) ?? null)
  const openSignIn = useEffectEvent((account: MerchantAccount) => {
    signIn(account)
    const next = new URLSearchParams(params)
    next.delete('sign-in')
    setParams(next, { replace: true })
  })
  useEffect(() => {
    if (signingIn !== null && canSignIn) openSignIn(signingIn)
  }, [signingIn, canSignIn])

  // `?backfill=<account>` is the link the assistant hands on, since starting
  // a backfill is the person's call: it opens that account's confirm until
  // the confirm closes.
  const backfillFor = params.get('backfill')
  const confirming =
    backfilling ??
    (backfillFor === null ? null : (accounts.find((one) => one.id === backfillFor) ?? null))
  const closeBackfill = () => {
    setBackfilling(null)
    if (backfillFor === null) return
    const next = new URLSearchParams(params)
    next.delete('backfill')
    setParams(next, { replace: true })
  }

  const pullNow = (account: MerchantAccount, days?: number, backTo?: string) => {
    void progress(
      `Updating ${account.name}…`,
      pull.mutateAsync({ id: account.id, days }),
      (result) => ({
        title: `${account.name}: ${plural(result.new_orders, `new ${noun}`, `new ${nounPlural}`)}${
          backTo ? ` back to ${backTo}` : ''
        }, ${plural(result.matched, 'bank row')} matched`,
        description: result.warnings[0],
      }),
      (error) =>
        connectorFailureToast({
          title: `${account.name} was not updated`,
          description: describeMerchantPullFailure(merchant, error),
          provider: name,
          screenshot: merchantPullFailureScreenshot(merchant, account.id, error),
        }),
    )
  }

  return (
    <Card
      title={`${name} accounts`}
      subtitle={`Sign in once; ${nounPlural} are read daily.`}
      actions={
        <Button
          size="sm"
          variant="secondary"
          disabled={create.isPending}
          onClick={() =>
            // With nothing to sign in with, an account is only somewhere to
            // file imports under, and needs no dialog to be made.
            canSignIn ? signIn(null) : create.mutate('')
          }
        >
          <Plus size={14} aria-hidden="true" /> Add account
        </Button>
      }
    >
      <EngineNotice
        engine={engine}
        after={
          !canSignIn && files.length > 0
            ? `${name} ${nounPlural} can still be imported from files below.`
            : undefined
        }
      />
      {loading ? (
        <SkeletonRows rows={2} />
      ) : accounts.length === 0 ? (
        <EmptyState compact title={`No ${name} accounts yet.`} />
      ) : (
        <ConnectorList>
          {accounts.map((account) => (
            <AccountRow
              key={account.id}
              merchant={merchant}
              account={account}
              canSignIn={canSignIn}
              backfillOffer={backfillOffer}
              pulling={account.pulling}
              onPull={() => pullNow(account)}
              onHistory={() => setReaching(account)}
              onBackfill={() => setBackfilling(account)}
              onSignIn={() => signIn(account)}
              onForget={() => forget.ask({ id: account.id, name: account.name })}
              onForgetPassword={() => forgetPassword.ask(account)}
              onEdit={() => setEditing(account)}
              onRemove={() => {
                if (account.orders === 0) remove.mutate(account.id)
                else removing.ask(account)
              }}
            />
          ))}
        </ConnectorList>
      )}
      {editing ? (
        <NameDialog
          open
          onOpenChange={(open) => (open ? undefined : setEditing(null))}
          title={`Rename ${editing.name}`}
          description="What this login is called here."
          label="Name (optional)"
          hint={
            editing.email
              ? `Left empty, it is called ${editing.email}.`
              : 'Only needed to tell two logins at one shop apart.'
          }
          placeholder="Alex"
          maxLength={80}
          optional
          initial={editing.label}
          onSubmit={(label) => rename.mutateAsync({ id: editing.id, label })}
        />
      ) : null}
      {reaching ? (
        <PullHistory
          merchant={merchant}
          account={reaching}
          busy={accounts.find((one) => one.id === reaching.id)?.pulling ?? false}
          onPull={(days, backTo) => pullNow(reaching, days, backTo)}
          onClose={() => setReaching(null)}
        />
      ) : null}
      {confirming ? (
        <BackfillDialog
          merchant={merchant}
          account={confirming}
          onStarted={() =>
            show({
              title: `${confirming.name}: backfilling invoices`,
              description: 'Its progress shows on the account.',
            })
          }
          onClose={closeBackfill}
        />
      ) : null}
      <ForgetSignInConfirm confirm={forget} kept={`${capitalize(nounPlural)} on file`} />
      <ConfirmDialog
        {...forgetPassword.dialog}
        title={
          forgetPassword.target
            ? `Forget ${forgetPassword.target.name}'s password?`
            : 'Forget the password?'
        }
        description={
          forgetPassword.target?.has_totp
            ? 'Deletes the password and authenticator key. The session stays; sign in again when it lapses.'
            : 'Deletes the password. The session stays; sign in again when it lapses.'
        }
        confirmLabel={forgetPasswordLabel(forgetPassword.target?.has_totp ?? false)}
      />
      <ConfirmDialog
        {...removing.dialog}
        title={removing.target ? `Remove ${removing.target.name}?` : 'Remove this account?'}
        description={
          removing.target
            ? `Deletes its ${plural(removing.target.orders, noun, nounPlural)} on file. Matched bank rows keep their categories.`
            : undefined
        }
        confirmLabel="Remove account"
      />
    </Card>
  )
}

/** One login at the shop. The Daily switch stays on the row beside the action. */
function AccountRow({
  merchant,
  account,
  canSignIn,
  backfillOffer,
  pulling: pullRunning,
  onPull,
  onHistory,
  onBackfill,
  onSignIn,
  onForget,
  onForgetPassword,
  onEdit,
  onRemove,
}: {
  merchant: MerchantId
  account: MerchantAccount
  canSignIn: boolean
  backfillOffer: BackfillOffer
  pulling: boolean
  onPull: () => void
  onHistory: () => void
  onBackfill: () => void
  onSignIn: () => void
  onForget: () => void
  onForgetPassword: () => void
  onEdit: () => void
  onRemove: () => void
}) {
  const { name, noun, nounPlural, hasGiftCardBalance, files } = MERCHANTS[merchant]
  const update = useUpdateMerchantAccount(merchant)
  const { primary, menu } = accountActions(account, canSignIn, backfillOffer)
  const backfillRunning = account.backfill?.running ?? false
  // A backfill holds the account's pull claim, so nothing that pulls can run beside it.
  const pulling = pullRunning || backfillRunning
  const backfillLine = describeBackfill(merchant, account.backfill)
  const stopped = accountStopped(account)
  const need = accountSignInNeed(account)
  const login = signsInAs(account)
  const password = passwordFact(account, name)

  const button: ReactNode =
    primary === 'connect' ? (
      <SignInButton label={signInLabel(account.connected, need)} onClick={onSignIn} />
    ) : primary === 'update' ? (
      <RefreshButton
        busy={pullRunning}
        label="Update now"
        busyLabel="Updating…"
        disabled={backfillRunning}
        onClick={onPull}
      />
    ) : null

  const entries: Record<AccountMenuAction, OverflowAction> = {
    update: {
      label: 'Update now',
      icon: <RefreshCw size={14} />,
      disabled: pulling,
      onSelect: onPull,
    },
    history: {
      label: 'Fetch history…',
      icon: <History size={14} />,
      disabled: pulling,
      onSelect: onHistory,
    },
    backfill: {
      label: backfillRunning ? 'Backfilling invoices…' : 'Backfill invoices…',
      icon: <FileText size={14} />,
      disabled: pulling,
      onSelect: onBackfill,
    },
    connect: {
      label: signInLabel(account.connected, need),
      icon: <LogIn size={14} />,
      onSelect: onSignIn,
    },
    'forget-password': {
      label: forgetPasswordLabel(account.has_totp),
      icon: <KeyRound size={14} />,
      onSelect: onForgetPassword,
    },
    forget: { label: 'Forget session', icon: <Unplug size={14} />, onSelect: onForget },
    edit: { label: 'Rename', icon: <Pencil size={14} />, onSelect: onEdit },
    remove: {
      label: 'Remove',
      icon: <Trash2 size={14} />,
      danger: true,
      onSelect: onRemove,
    },
  }

  return (
    <ConnectorRow
      title={account.name}
      badge={
        <ConnectorBadge
          need={need}
          needsSignIn={account.needs_sign_in}
          failed={account.last_sync_status === 'failed'}
          connected={account.connected}
          idle="Not signed in"
        />
      }
      meta={[
        plural(account.orders, noun, nounPlural),
        stopped
          ? account.last_synced_at
            ? `Updated ${timeAgo(account.last_synced_at, 'long')}`
            : null
          : account.connected
            ? account.pulling && account.progress
              ? <PullProgressLine progress={account.progress} fallback={describeSync(merchant, account)} />
              : describeSync(merchant, account)
            : files.length > 0
              ? `${capitalize(nounPlural)} arrive from files only`
              : null,
        !stopped && account.connected && account.last_synced_at
          ? `Updated ${timeAgo(account.last_synced_at, 'long')}`
          : null,
        backfillRunning ? backfillLine : null,
        hasGiftCardBalance && account.gift_card_account_id ? (
          <>
            <Link
              to={`/transactions?displayNode=${encodeURIComponent(account.gift_card_account_id)}`}
            >
              Gift card balance
            </Link>{' '}
            {account.gift_card_balance !== null ? (
              <Money value={account.gift_card_balance} tone="neutral" />
            ) : (
              'unknown'
            )}
            {account.gift_card_balance_at
              ? ` as of ${timeAgo(account.gift_card_balance_at, 'long')}`
              : ''}
          </>
        ) : null,
      ]}
      aside={
        account.connected ? (
          <Switch
            label="Daily"
            labelPosition="before"
            checked={account.sync_enabled}
            disabled={update.isPending}
            onCheckedChange={(checked) => update.mutate({ id: account.id, sync_enabled: checked })}
          />
        ) : null
      }
      primary={button}
      actions={menu.map((key) => entries[key])}
      actionsLabel={`Actions for ${account.name}`}
      note={
        need === 'password-signs-in' ? (
          <SessionExpiredNote />
        ) : stopped && account.last_sync_error !== '' ? (
          <ConnectorFailureNote
            raw={account.last_sync_error}
            provider={name}
            screenshot={merchantFailureScreenshot(merchant, account)}
          />
        ) : null
      }
      details={
        <>
          {login !== null ? <ConnectorFact>Signs in as {login}</ConnectorFact> : null}
          {password !== null ? (
            <ConnectorFact icon={<ShieldCheck size={13} aria-hidden="true" />}>
              {password}
            </ConnectorFact>
          ) : null}
          {!stopped && account.last_sync_error !== '' ? (
            <ConnectorFact>
              The last update finished with a warning: {account.last_sync_error}
            </ConnectorFact>
          ) : null}
          {!backfillRunning && backfillLine !== null ? (
            <ConnectorFact icon={<FileText size={13} aria-hidden="true" />}>
              {backfillLine}
            </ConnectorFact>
          ) : null}
        </>
      }
    />
  )
}
