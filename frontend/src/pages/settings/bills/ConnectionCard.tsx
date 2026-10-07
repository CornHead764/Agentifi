import {
  ChevronDown,
  ChevronRight,
  ExternalLink,
  History,
  KeyRound,
  LogIn,
  Mail,
  Pencil,
  RefreshCw,
  ShieldCheck,
  Trash2,
  Unplug,
} from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { ConnectorFailureNote } from '@/components/ConnectorFailureNote'
import { connectorFailureToast } from '@/components/failure-screenshot'
import {
  Button,
  Callout,
  ConfirmDialog,
  EmptyState,
  type OverflowAction,
  useConfirm,
  useProgressToast,
  useToast,
} from '@/components/ui'
import { billerById, billerName, connectionTitle } from '@/lib/billers'
import {
  billFailureScreenshot,
  describeAutopay,
  describePullResult,
  describePullStatus,
  useForgetBillCredential,
  useForgetBillSession,
  usePullBillConnection,
  type BillAgentStatus,
  type BillChallenge,
  type BillConnection,
  type BillProviderInfo,
  type BillSubaccount,
} from '@/lib/clients/bills'
import { plural, timeAgo } from '@/lib/format'

import {
  ConnectorBadge,
  RefreshButton,
  SessionExpiredNote,
  SignInButton,
} from '../connector/ConnectorControls'
import { ConnectorFact, ConnectorRow } from '../connector/ConnectorRow'
import { billEngine } from '../connector/engine'
import { ForgetSignInConfirm } from '../connector/ForgetSignInConfirm'
import { useForgetSignIn } from '../connector/useForgetSignIn'
import { keptPasswordFact } from '../connector/secondFactor'
import { forgetPasswordLabel, signInLabel } from '../connector/signInNeed'
import { splitSubaccounts } from './draft'
import {
  connectionActions,
  menuActionLabel,
  scheduleNote,
  signInBlocked,
  connectionSignInNeed,
  waitingChallengeFor,
  type MenuAction,
} from './status'
import { KeptTrail } from './ProviderTrail'
import { SubaccountRow } from './SubaccountRow'
import { useMatchHistory } from './useMatchHistory'

/**
 * One login at one provider, and the accounts it bills for. The accounts come
 * from the provider, never from a form: a bill typed by hand is a recurring
 * transaction. The card's state chooses the one button: a provider waiting for
 * a code is signed in, so it is offered the code, not a sign-in.
 */
export function ConnectionCard({
  connection,
  subaccounts,
  agent,
  provider,
  challenges,
  onEdit,
  onDelete,
  onConnect,
  onWriteMailRule,
  onChallenge,
}: {
  connection: BillConnection
  subaccounts: readonly BillSubaccount[]
  /** Undefined while the agent is still being asked; null providers mean it has none. */
  agent: BillAgentStatus | undefined
  provider: BillProviderInfo | null
  challenges: readonly BillChallenge[]
  onEdit: () => void
  onDelete: () => void
  onConnect: () => void
  /** For a company tracked from its e-mailed bills: the rule that files them. */
  onWriteMailRule?: () => void
  onChallenge: (challengeId: string) => void
}) {
  const { show } = useToast()
  const progress = useProgressToast()
  const biller = billerById(connection.biller)
  const [showingHidden, setShowingHidden] = useState(false)
  const pull = usePullBillConnection()
  // The server's word, which covers the pull a sign-in starts as well as this
  // page's: a second would only be refused behind it.
  const updating = connection.pulling
  const forget = useForgetSignIn(useForgetBillSession())
  const forgetPassword = useConfirm(useForgetBillCredential(), {
    onSuccess: () => show({ title: `Forgot the ${billerName(connection.biller)} password` }),
  })
  const history = useMatchHistory(billerName(connection.biller))
  const keptPassword = connection.credential_source === 'stored'
  const keptKey = keptPassword && connection.has_totp

  // A provider nobody can sign in to bills by mail or on the account sync; the
  // card says which rather than offering a button that could only refuse.
  const access = provider?.access ?? biller?.access ?? 'browser'
  const bridged = access === 'api' || access === 'browser'
  const blocked = bridged ? signInBlocked(agent, provider) : null
  // The engine's own state is said once above the cards; a card names only a
  // reason that is this provider's.
  const ownReason = billEngine(agent).kind === 'ready' ? blocked : null
  const waiting = waitingChallengeFor(connection.id, challenges)
  const medical = biller?.medical === true
  // A portal deployed per customer is at the connection's own address.
  const website =
    biller?.site?.address === true && connection.site !== ''
      ? connection.site
      : biller && biller.homeUrl !== ''
        ? biller.homeUrl
        : null
  const actions = connectionActions(connection, challenges, bridged, website !== null)
  const primary = actions.primary
  // Matching history pairs bills with a reminder's payments; a medical
  // provider's payments are paired with the bank on their own.
  const menu = medical ? actions.menu.filter((key) => key !== 'match-history') : actions.menu
  const need = connectionSignInNeed(connection)
  const schedule = scheduleNote(connection, provider)
  const { shown, hidden } = splitSubaccounts(subaccounts)
  const title = connectionTitle(connection)
  const providerName = billerName(connection.biller)
  // A stopped login's badge already says it stopped, so its facts line keeps
  // to when it last worked and the note underneath says why.
  const stopped =
    connection.needs_sign_in ||
    connection.last_pull_status === 'needs_sign_in' ||
    connection.last_pull_status === 'failed' ||
    connection.last_pull_status === 'sign_in_failed'
  const troubled = stopped && connection.last_pull_error !== ''
  const failureNoted =
    troubled && need !== 'password-signs-in' && !(need === 'code-needed' && waiting === null)
  // A new update or sign-in is a new trail, so the kept one is read again.
  const trailKey = `${connection.last_pulled_at ?? ''}|${connection.last_pull_status}|${connection.last_pull_error}`
  const pulled = !bridged
    ? biller?.generic === true
      ? 'Bills arrive through a mail rule'
      : 'Bills arrive by email'
    : stopped
      ? connection.last_pulled_at === null
        ? null
        : `Updated ${timeAgo(connection.last_pulled_at, 'long')}`
      : connection.connected
        ? describePullStatus(connection)
        : null

  const runPull = async () => {
    const result = await progress(
      `Updating ${providerName}…`,
      pull.mutateAsync(connection.id),
      (result) =>
        result.status === 'failed' || result.status === 'needs_sign_in'
          ? connectorFailureToast({
              title: `${providerName} was not updated`,
              description: describePullResult(result, providerName),
              provider: providerName,
              screenshot: billFailureScreenshot({
                id: connection.id,
                has_failure_screenshot: result.has_failure_screenshot,
              }),
            })
          : { title: describePullResult(result, providerName), description: result.notes[0] },
      `${providerName} was not updated`,
    )
    if (result?.status === 'challenge' && result.challenge) onChallenge(result.challenge.id)
  }

  const button: ReactNode =
    primary === 'challenge' && waiting !== null ? (
      <Button size="sm" variant="primary" onClick={() => onChallenge(waiting.id)}>
        <KeyRound size={14} aria-hidden="true" /> Answer the code request
      </Button>
    ) : primary === 'connect' ? (
      <SignInButton
        label={signInLabel(connection.connected, need)}
        disabled={blocked !== null}
        title={blocked ?? undefined}
        onClick={onConnect}
      />
    ) : primary === 'update' ? (
      <RefreshButton
        busy={updating}
        label="Update now"
        busyLabel="Updating…"
        disabled={blocked !== null}
        title={blocked ?? undefined}
        onClick={runPull}
      />
    ) : null

  const entries: Record<MenuAction, Omit<OverflowAction, 'label'>> = {
    update: {
      icon: <RefreshCw size={14} />,
      disabled: blocked !== null || updating,
      onSelect: runPull,
    },
    connect: { icon: <LogIn size={14} />, disabled: blocked !== null, onSelect: onConnect },
    'forget-password': {
      icon: <KeyRound size={14} />,
      disabled: forgetPassword.dialog.pending,
      onSelect: () => forgetPassword.ask(connection.id),
    },
    'forget-session': {
      icon: <Unplug size={14} />,
      disabled: forget.dialog.pending,
      onSelect: () => forget.ask({ id: connection.id, name: providerName }),
    },
    'match-history': {
      icon: <History size={14} />,
      disabled: history.pending || subaccounts.length === 0,
      onSelect: () => void history.run({ connectionId: connection.id }),
    },
    edit: { icon: <Pencil size={14} />, onSelect: onEdit },
    website: { icon: <ExternalLink size={14} />, href: website ?? undefined },
    remove: { icon: <Trash2 size={14} />, danger: true, onSelect: onDelete },
  }

  return (
    <ConnectorRow
      title={title}
      badge={
        <ConnectorBadge
          need={need}
          waiting={waiting !== null}
          needsSignIn={connection.needs_sign_in}
          failed={
            connection.last_pull_status === 'failed' ||
            connection.last_pull_status === 'sign_in_failed'
          }
          connected={connection.connected}
          idle="Not connected"
        />
      }
      meta={[describeAutopay(connection), pulled]}
      primary={button}
      actions={menu.map((key) => ({
        ...entries[key],
        label: menuActionLabel(key, connection, providerName),
      }))}
      actionsLabel={`Actions for ${title}`}
      note={
        need === 'password-signs-in' ? (
          <SessionExpiredNote />
        ) : need === 'code-needed' && waiting === null ? (
          <Callout tone="warning" role="alert">
            {providerName} asked for a code. Automatic updates wait until you sign in.
          </Callout>
        ) : failureNoted ? (
          <ConnectorFailureNote
            raw={connection.last_pull_error}
            provider={providerName}
            screenshot={billFailureScreenshot(connection)}
          >
            {connection.has_trail ? <KeptTrail key={trailKey} connectionId={connection.id} /> : null}
          </ConnectorFailureNote>
        ) : ownReason !== null ? (
          <p className="connector__reason muted">{ownReason}</p>
        ) : null
      }
      details={
        <>
          {!bridged ? (
            <ConnectorFact>
              {biller?.generic === true ? (
                <>
                  {`Bills arrive by email; a mail rule files each one here.`}{' '}
                  {onWriteMailRule ? (
                    <Button size="sm" variant="ghost" onClick={onWriteMailRule}>
                      <Mail size={13} aria-hidden="true" /> Write its mail rule
                    </Button>
                  ) : null}
                </>
              ) : (
                <>
                  {`${providerName}'s bills come by email: `}
                  <Link to="/settings/email">the mailbox</Link>
                  {' reads them; no sign-in needed.'}
                </>
              )}
            </ConnectorFact>
          ) : null}
          {keptPassword ? (
            <ConnectorFact icon={<ShieldCheck size={13} aria-hidden="true" />}>
              {need === 'password-refused'
                ? `Password kept, encrypted, but ${providerName} refused it. Not retried until you sign in or press Update now.`
                : need === 'code-needed'
                  ? 'Password kept, encrypted; updates resume on their own after you sign in.'
                  : keptPasswordFact(connection)}
            </ConnectorFact>
          ) : bridged && connection.connected ? (
            <ConnectorFact icon={<ShieldCheck size={13} aria-hidden="true" />}>
              Session only; sign in again when it lapses.
            </ConnectorFact>
          ) : null}
          {schedule !== null ? <ConnectorFact>{schedule}</ConnectorFact> : null}
          {!stopped && connection.last_pull_error !== '' ? (
            <ConnectorFact>
              The last update finished with a warning: {connection.last_pull_error}
            </ConnectorFact>
          ) : null}
          {!failureNoted && connection.has_trail ? (
            <KeptTrail key={trailKey} connectionId={connection.id} />
          ) : null}
        </>
      }
    >
      {subaccounts.length === 0 ? (
        <EmptyState
          compact
          title={
            bridged
              ? 'Nothing billed under this login yet. Sign in to list its accounts.'
              : 'Nothing billed under this login yet. The mailbox names what it bills for.'
          }
        />
      ) : (
        <ul className="bill-subaccounts">
          {shown.map((one) => (
            <SubaccountRow key={one.id} subaccount={one} provider={providerName} medical={medical} />
          ))}
          {hidden.length > 0 ? (
            <li className="bill-subaccounts__fold">
              <Button
                size="sm"
                variant="ghost"
                aria-expanded={showingHidden}
                onClick={() => setShowingHidden(!showingHidden)}
              >
                {showingHidden ? (
                  <ChevronDown size={14} aria-hidden="true" />
                ) : (
                  <ChevronRight size={14} aria-hidden="true" />
                )}{' '}
                {plural(hidden.length, 'hidden account')}
              </Button>
            </li>
          ) : null}
          {showingHidden
            ? hidden.map((one) => (
                <SubaccountRow key={one.id} subaccount={one} provider={providerName} medical={medical} />
              ))
            : null}
        </ul>
      )}

      <ConfirmDialog
        {...forgetPassword.dialog}
        title={
          keptKey
            ? `Forget the ${providerName} password and key?`
            : `Forget the ${providerName} password?`
        }
        description={
          keptKey
            ? 'Drops the password, authenticator key and session. Updates stop until someone signs in again. Bills on file stay.'
            : 'Drops the password and session. Updates stop until someone signs in again. Bills on file stay.'
        }
        confirmLabel={forgetPasswordLabel(keptKey)}
      />
      <ForgetSignInConfirm confirm={forget} kept="Bills on file" />
    </ConnectorRow>
  )
}
