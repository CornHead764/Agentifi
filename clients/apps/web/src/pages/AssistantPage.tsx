import {
  Bot,
  CheckCheck,
  CircleAlert,
  History,
  Mail,
  NotebookPen,
  Plus,
  Sparkles,
  Trash2,
  X,
  Zap,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Button,
  Card,
  ConfirmDialog,
  EmptyState,
  IconButton,
  List,
  ListRow,
  PageHeader,
  Spinner,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  useConfirm,
} from '@/components/ui'
import {
  conversationLabel,
  nextActiveConversation,
} from '@/lib/assistant/history'
import {
  useApplyManyActions,
  useAssistantStatus,
  useConversation,
  useConversations,
  useDeleteConversation,
  useDiscardManyActions,
  useStartMailConversation,
  type AssistantStatus,
  type Conversation,
  type ConversationMail,
} from '@/lib/clients/assistant'
import { usePendingAutomationActions } from '@/lib/clients/automations'
import { useOwnsSpace } from '@/lib/clients/spaces'
import { reviewQueueLink } from '@/lib/transactions/links'
import type { Uuid } from '@/lib/transactions/types'

import type { EditorSeed } from './assistant/AutomationEditor'
import { AutomationsPanel, NewAutomationMenu } from './assistant/AutomationsPanel'
import { ChangesSwitches, ConnectionDialog, EnabledSwitch, Setup } from './assistant/connection'
import { waiting } from './assistant/cards'
import { Thread } from './assistant/thread'
import { formatTimestamp, plural, timeAgo } from '@/lib/format'

/**
 * Asking about the ledger, and asking it to change something.
 *
 *   - **It shows what was read.** Every tool call is on screen under the
 *     answer it produced, so a figure can be checked.
 *   - **It is off until it is set up.** Nothing about this household is sent
 *     anywhere until a person has said where.
 *   - **A change is a card, not a side effect.** The card shows the exact
 *     request and a person applies it.
 *
 * The provider is set up here too: this page names the model and opens the
 * connection form, with what the assistant can do, in a dialog.
 */
export function AssistantPage() {
  const status = useAssistantStatus()

  return (
    <div className="page">
      <QueryBoundary query={status} rows={3}>
        {(data) =>
          data.configured ? <Configured status={data} /> : <Setup />
        }
      </QueryBoundary>
    </div>
  )
}

/**
 * Two tabs once a model is configured: the conversation, and the automations
 * that run the same assistant on a trigger. The strip also links to the
 * guidance beside the rules. Suggestions are decided on the register's rows.
 */
function Configured({
  status,
}: {
  status: AssistantStatus
}) {
  const pending = usePendingAutomationActions()
  const waiting = pending.data?.length ?? 0
  const [picked, setPicked] = useState<string | null>(null)
  const [seed, setSeed] = useState<EditorSeed | null>(null)
  const [params, setParams] = useSearchParams()
  const newChat = () =>
    setParams((current) => {
      const merged = new URLSearchParams(current)
      merged.delete('conversation')
      return merged
    })
  return (
    <Tabs value={picked ?? 'chat'} onValueChange={setPicked}>
      <PageHeader
        tabs={
          <TabsList aria-label="Assistant">
            <TabsTrigger value="chat">
              <Bot size={14} aria-hidden="true" /> Chat
            </TabsTrigger>
            <TabsTrigger value="automations">
              <Zap size={14} aria-hidden="true" /> Automations
            </TabsTrigger>
          </TabsList>
        }
        actions={
          <>
            {/* Not a tab that navigates: a strip where two items switch a
                panel and the third leaves the page is a strip somebody
                presses Back out of. */}
            <Button size="sm" asChild>
              <Link to="/rules/guidance">
                <NotebookPen size={13} aria-hidden="true" /> Guidance
              </Link>
            </Button>
            {picked === 'automations' ? (
              <NewAutomationMenu onNew={setSeed} />
            ) : (
              <Button
                variant="primary"
                size="sm"
                onClick={newChat}
                disabled={!params.has('conversation')}
              >
                <Plus size={13} aria-hidden="true" /> New conversation
              </Button>
            )}
          </>
        }
      />
      <TabsContent value="chat">
        <Chat status={status} automationsWaiting={waiting} />
      </TabsContent>
      <TabsContent value="automations">
        <AutomationsPanel
          status={status}
          editing={seed}
          onEditingChange={setSeed}
        />
      </TabsContent>
    </Tabs>
  )
}

function Chat({
  status,
  automationsWaiting,
}: {
  status: AssistantStatus
  /** Pending actions proposed by automations, which are in other conversations
   *  and are not what the line under the question box counts. */
  automationsWaiting: number
}) {
  // Which thread is open is in the URL, and nothing is open by default: a
  // visit here is a new chat. A new chat exists only in the browser (`useAsk`
  // writes the row when the first question is sent), so clicking away leaves
  // nothing behind.
  const [params, setParams] = useSearchParams()
  const id: Uuid | null = params.get('conversation')
  const openThread = (next: Uuid | null, replace = false) =>
    setParams(
      (current) => {
        const merged = new URLSearchParams(current)
        if (next === null) merged.delete('conversation')
        else merged.set('conversation', next)
        return merged
      },
      { replace },
    )
  const conversations = useConversations()
  const remove = useConfirm(useDeleteConversation(), {
    variables: (target: Conversation) => target.id,
    onSuccess: (_result, target) =>
      openThread(nextActiveConversation(conversations.data ?? [], id, target.id), true),
  })
  const conversation = useConversation(id)

  // `?mail=<log row>` is the Email page's "Ask the assistant": the thread is
  // started straight away, and the parameter is swapped for the thread's own so
  // a reload does not start a second one.
  const mailId = params.get('mail')
  const startMail = useStartMailConversation()
  const startedFor = useRef<string | null>(null)
  useEffect(() => {
    if (mailId === null || startedFor.current === mailId) return
    startedFor.current = mailId
    startMail.mutate(mailId, {
      onSuccess: (made) =>
        setParams(
          (current) => {
            const merged = new URLSearchParams(current)
            merged.delete('mail')
            merged.set('conversation', made.id)
            return merged
          },
          { replace: true },
        ),
    })
  }, [mailId, startMail, setParams])

  const [changingModel, setChangingModel] = useState(false)
  const ownsSpace = useOwnsSpace()

  const pending = waiting(conversation.data?.actions ?? [])

  return (
    <div className="split split--cashflow">
      {status.is_enabled ? (
        <Card
          title={
            <>
              <Bot size={16} aria-hidden="true" /> Ask about your money
            </>
          }
          subtitle={
            <>
              {status.model} · {describeWhatItCanDo(status)}
              {ownsSpace ? (
                <>
                  {' · '}
                  <Button variant="ghost" size="sm" onClick={() => setChangingModel(true)}>
                    Change the model
                  </Button>
                </>
              ) : null}
            </>
          }
          actions={<ChangesSwitches status={status} />}
        >
          {conversation.data?.mail ? <MailChip mail={conversation.data.mail} /> : null}
          {startMail.isPending ? <p className="muted">Attaching the email…</p> : null}
          <Thread
            status={status}
            conversationId={id}
            // Replacing rather than pushing: Back should leave the assistant
            // rather than return to the empty chat the question was typed in.
            onThread={(made) => openThread(made, true)}
            ideas={suggestions(status)}
            intro={<p className="muted">New conversation. Saved once you send something.</p>}
            placeholder={
              status.allow_writes
                ? 'Ask, or tell it what to change — “make a rule for…”'
                : 'What did we spend the most on last month?'
            }
          >
            {id !== null && pending.length > 1 ? (
              <PendingBar
                conversationId={id}
                pending={pending.map((one) => one.id)}
              />
            ) : null}
          </Thread>

          <p className="hint">
            <CircleAlert size={13} aria-hidden="true" /> {footnote(status, pending.length)}
          </p>
          {/* What the automations proposed elsewhere, on its own line: the
              footnote above is about this conversation's changes. */}
          {automationsWaiting > 0 ? (
            <p className="hint">
              <Sparkles size={13} aria-hidden="true" /> Automations proposed {automationsWaiting}{' '}
              {automationsWaiting === 1 ? 'change' : 'changes'}.{' '}
              <Link to={reviewQueueLink()}>Review {automationsWaiting === 1 ? 'it' : 'them'}</Link>
            </p>
          ) : null}
        </Card>
      ) : (
        <OffNotice status={status} />
      )}

      <div className="stack">
        <Card
          title={
            <>
              <History size={16} aria-hidden="true" /> Conversations
            </>
          }
        >
          <QueryBoundary
            query={conversations}
            rows={4}
            empty={(rows) =>
              rows.length === 0 ? (
                <EmptyState
                  title="No conversations yet"
                  body="Ask something on the left to start one."
                />
              ) : null
            }
          >
            {(rows) => (
              <List dividers={false}>
                {rows.map((one) => (
                  <ListRow
                    key={one.id}
                    title={<span title={conversationLabel(one)}>{conversationLabel(one)}</span>}
                    sub={timeAgo(one.updated_at, 'long')}
                    current={one.id === id}
                    onSelect={() => openThread(one.id)}
                    actions={
                      <IconButton
                        label={`Delete ${conversationLabel(one)}`}
                        variant="ghost"
                        size="sm"
                        disabled={remove.dialog.pending}
                        onClick={() => remove.ask(one)}
                      >
                        <Trash2 size={13} />
                      </IconButton>
                    }
                  />
                ))}
              </List>
            )}
          </QueryBoundary>
        </Card>

      </div>

      <ConnectionDialog
        status={status}
        open={changingModel}
        onClose={() => setChangingModel(false)}
      />
      <ConfirmDialog
        {...remove.dialog}
        title={
          remove.target ? `Delete ${conversationLabel(remove.target)}?` : 'Delete conversation?'
        }
        description="Deletes its messages and tool calls. Changes already applied stay."
        confirmLabel="Delete conversation"
      />
    </div>
  )
}

/** What a new conversation opens on: what the assistant can do, as sentences to press and edit. */
function suggestions(status: AssistantStatus): string[] {
  return status.allow_writes
    ? [
        'Make a rule that files anything from the coffee shop under Dining',
        'Categorize last month’s uncategorized transactions',
        'Set my groceries watchlist to $400 a month',
        'What did we spend the most on last month?',
      ]
    : [
        'What did we spend the most on last month?',
        'Are we spending more than we earn this year?',
        'What bills are due before the end of the month?',
      ]
}

/**
 * Every card still waiting in this conversation, decided at once, when there
 * is more than one. The server applies them in proposal order, so a rule
 * naming a category proposed beside it runs after the category.
 */
function PendingBar({
  conversationId,
  pending,
}: {
  conversationId: Uuid
  pending: readonly Uuid[]
}) {
  const applyMany = useApplyManyActions()
  const discardMany = useDiscardManyActions()
  const busy = applyMany.isPending || discardMany.isPending
  return (
    <div className="chat__pending" role="region" aria-label="Proposed changes">
      <span>
        <strong>{pending.length}</strong> proposed changes waiting
      </span>
      <span className="toolbar__spacer" />
      {busy ? (
        <span className="chat__change-running" role="status">
          <Spinner size={13} /> Applying…
        </span>
      ) : (
        <>
          <Button
            variant="primary"
            size="sm"
            onClick={() => applyMany.mutate({ ids: pending, conversationId })}
          >
            <CheckCheck size={13} aria-hidden="true" /> Accept all
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => discardMany.mutate({ ids: pending, conversationId })}
          >
            <X size={13} aria-hidden="true" /> Decline all
          </Button>
        </>
      )}
    </div>
  )
}

/**
 * What stands in for the chat box while the assistant is off. The switch and
 * the model form are here as well as in settings: the person arrived wanting
 * to ask something.
 */
function OffNotice({
  status,
}: {
  status: AssistantStatus
}) {
  const [changingModel, setChangingModel] = useState(false)
  const ownsSpace = useOwnsSpace()
  return (
    <Card
      title={
        <>
          <Bot size={16} aria-hidden="true" /> The assistant is switched off
        </>
      }
      subtitle="Nothing is sent and no automation runs. Connection, key and conversations are kept."
      actions={<EnabledSwitch status={status} />}
    >
      {ownsSpace ? (
        <p className="muted">
          Turn it on above, or{' '}
          <Button variant="ghost" size="sm" onClick={() => setChangingModel(true)}>
            change the model
          </Button>
        </p>
      ) : (
        <p className="muted">Turn it on above. The space&rsquo;s owner chooses the model.</p>
      )}
      <ConnectionDialog
        status={status}
        open={changingModel}
        onClose={() => setChangingModel(false)}
      />
    </Card>
  )
}

/**
 * The email a thread is about: sender, subject and date. Its text is not
 * shown; the server hands it to the model per question as data to read, never
 * instructions to follow.
 */
function MailChip({ mail }: { mail: ConversationMail }) {
  return (
    <p className="chat__attachment" role="note">
      <Mail size={13} aria-hidden="true" />
      <span>
        About the email: <strong>{mail.subject || '(no subject)'}</strong> — from {mail.sender},{' '}
        {formatTimestamp(mail.received_at, 'date')}
      </span>
    </p>
  )
}

/** What the assistant can do, in the words the header uses. */
function describeWhatItCanDo(status: AssistantStatus): string {
  if (!status.allow_writes) return 'read-only'
  if (status.apply_without_asking) return 'applies changes without asking'
  return 'proposes changes for you to accept'
}

/**
 * The line under the question box, which says what the two switches mean.
 * Changes waiting outrank the explanation; with automatic application on
 * nothing waits, so it says where the record is.
 *
 * `pending` is this conversation's own, not the Automations tab's count of
 * every pending automation action, and the line says which it counts.
 */
function footnote(status: AssistantStatus, pending: number): string {
  if (pending > 0) {
    return `${plural(pending, 'proposed change')} waiting in this conversation. Nothing changed yet.`
  }
  if (!status.allow_writes) return 'Explains the numbers. Not investment advice.'
  if (status.apply_without_asking) {
    return 'Changes apply immediately. Each is logged here with its exact request.'
  }
  return 'Changes arrive as cards to accept or decline. Your decision goes with your next message.'
}
