import { Bot, RefreshCw, Wand2 } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { QueryBoundary } from '@/components/QueryBoundary'
import { StatementButton } from '@/components/StatementButton'
import {
  Button,
  Card,
  EmptyState,
  type OverflowAction,
  useProgressToast,
  useToast,
} from '@/components/ui'
import { ApiError } from '@/lib/api'
import { useAssistantStatus } from '@/lib/clients/assistant'
import { useBillConnections, type BillConnection } from '@/lib/clients/bills'
import {
  useRecentMail,
  useRereadMailboxMessage,
  useSuggestMailRule,
  type EmailConnection,
  type MailboxMessage,
  type MailRuleSuggestion,
} from '@/lib/clients/email'
import { describeApiError } from '@/lib/errors'
import { timeAgo } from '@/lib/format'

import { ConnectorList, ConnectorRow } from '../connector/ConnectorRow'
import { AssistantSetupDialog } from '@/pages/assistant/connection'

import { describeOutcome, mailProviderName, mailRowOffers, missingStatementNote } from './mailbox'
import { suggestedMailRuleForm } from './mailRule'
import { MailRuleDialog } from './MailRuleDialog'

/**
 * The last fifty messages across every mailbox, and what the app made of each.
 * The assistant items are never offered for a message that carried a sign-in
 * code, and are switched off while no assistant is ready. A drafted rule opens
 * in the ordinary rule dialog, unsaved.
 */
export function RecentMail({
  connections,
}: {
  connections: readonly EmailConnection[] | undefined
}) {
  const messages = useRecentMail(connections)
  const providers = useBillConnections()
  const status = useAssistantStatus()
  const assistantReady = status.data?.configured === true && status.data.is_enabled
  const labels = new Map((connections ?? []).map((one) => [one.id, one.label]))
  const [suggested, setSuggested] = useState<MailRuleSuggestion | null>(null)
  const [settingUp, setSettingUp] = useState(false)

  return (
    <Card
      title="Recent mail"
      subtitle="What arrived in the mailboxes, and what was done with each message."
    >
      {status.data && !assistantReady ? (
        <p className="hint">
          {status.data.configured
            ? 'The assistant is off, so it cannot explain mail or draft rules.'
            : 'Once set up, the assistant can explain mail and draft rules.'}{' '}
          <Button variant="secondary" size="sm" onClick={() => setSettingUp(true)}>
            {status.data.configured ? 'Turn the assistant on' : 'Set up the assistant'}
          </Button>
        </p>
      ) : null}
      <AssistantSetupDialog
        open={settingUp}
        onClose={() => setSettingUp(false)}
      />
      <QueryBoundary
        query={messages}
        rows={3}
        empty={(rows) =>
          rows.length === 0 ? (
            <EmptyState
              compact
              title="Nothing read yet. Forwarded mail shows up here after the next read."
            />
          ) : undefined
        }
      >
        {(rows) => (
          <ConnectorList>
            {rows.map((message) => (
              <MailRow
                key={message.id}
                message={message}
                mailbox={labels.size > 1 ? labels.get(message.connection_id) : undefined}
                providers={providers.data ?? []}
                assistantReady={assistantReady}
                onSuggested={setSuggested}
                onNeedsSetup={() => setSettingUp(true)}
              />
            ))}
          </ConnectorList>
        )}
      </QueryBoundary>

      {suggested ? (
        <MailRuleDialog
          rule={null}
          initial={suggestedMailRuleForm(suggested.rule)}
          sample={suggested.sample}
          notes={suggested.dropped}
          onClose={() => setSuggested(null)}
        />
      ) : null}
    </Card>
  )
}

function MailRow({
  message,
  mailbox,
  providers,
  assistantReady,
  onSuggested,
  onNeedsSetup,
}: {
  message: MailboxMessage
  /** The mailbox's label, named only when there is more than one. */
  mailbox: string | undefined
  /** The household's bill providers, which a filed bill is named by. */
  providers: readonly BillConnection[]
  assistantReady: boolean
  onSuggested: (suggestion: MailRuleSuggestion) => void
  /** The assistant turned out not to be ready: offer the setup. */
  onNeedsSetup: () => void
}) {
  const { show } = useToast()
  const navigate = useNavigate()
  const progress = useProgressToast()
  const reread = useRereadMailboxMessage()
  const suggest = useSuggestMailRule()
  const suggestFailed = (error: unknown) =>
    show({
      title: 'The assistant could not draft a rule',
      description: describeApiError(error),
      tone: 'error',
      action: needsAssistantSetup(error)
        ? { label: 'Set up the assistant', onSelect: onNeedsSetup }
        : undefined,
    })

  const offers = mailRowOffers(message, assistantReady)
  const subject = message.subject || '(no subject)'
  const provider = mailProviderName(message, providers)
  const unkept = missingStatementNote(message)

  const actions: OverflowAction[] = []
  if (offers.ask !== 'absent') {
    actions.push({
      label: 'Ask the assistant',
      icon: <Bot size={14} />,
      disabled: offers.ask === 'disabled',
      onSelect: () => void navigate(`/assistant?mail=${message.id}`),
    })
  }
  if (offers.suggest !== 'absent') {
    actions.push({
      label: suggest.isPending ? 'Drafting a rule…' : 'Suggest a rule',
      icon: <Wand2 size={14} />,
      disabled: offers.suggest === 'disabled' || suggest.isPending,
      onSelect: () => suggest.mutate(message.id, { onSuccess: onSuggested, onError: suggestFailed }),
    })
  }
  if (offers.reread) {
    actions.push({
      label: reread.isPending ? 'Reading…' : 'Read again',
      icon: <RefreshCw size={14} />,
      disabled: reread.isPending,
      onSelect: () =>
        void progress(
          `Reading ${subject}…`,
          reread.mutateAsync({ connectionId: message.connection_id, messageId: message.id }),
          (updated) => ({ title: describeOutcome(updated) }),
          (error) => ({ title: describeRereadFailure(error), tone: 'error' }),
        ),
    })
  }

  return (
    <ConnectorRow
      title={<span className="mailbox-log__subject">{subject}</span>}
      meta={[message.sender, timeAgo(message.received_at), mailbox]}
      note={
        <p className="mailbox-log__note">
          {suggest.isPending ? 'The assistant is drafting a rule… · ' : ''}
          {describeOutcome(message)}
          {provider === null ? '' : ` · ${provider}`}
          {unkept === null ? '' : ` · ${unkept}`}
          {message.document_id ? <StatementButton documentId={message.document_id} /> : null}
          {/* The log row has no account or date, so this is the narrow
              open-this-row deep link. */}
          {message.transaction_id ? (
            <Link to={`/transactions?edit=${message.transaction_id}`}>See the transaction</Link>
          ) : null}
        </p>
      }
      actions={actions}
      actionsLabel={`Actions for ${subject}`}
    />
  )
}

/** No model configured or switched off; the server says so with a code. */
function needsAssistantSetup(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'assistant_unavailable'
}

/** Why a reread did nothing, in the two ordinary cases the resource refuses. */
function describeRereadFailure(error: unknown): string {
  if (error instanceof ApiError && error.status === 409) return 'That mail has already been filed'
  if (error instanceof ApiError && error.status === 404)
    return 'That mail is no longer in the mailbox'
  return describeApiError(error, 'save')
}
