import { ChevronDown, ChevronUp, Send, Sparkles, Wrench } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'

import { Button, Spinner, Textarea } from '@/components/ui'
import { sendsOnEnter } from '@/lib/assistant/composer'
import { conversationTimeline, groupToolRuns, type GroupedEntry } from '@/lib/assistant/history'
import {
  useAsk,
  useConversation,
  writeToolNames,
  type AssistantMessage,
  type AssistantStatus,
} from '@/lib/clients/assistant'
import { plural } from '@/lib/format'
import { renderMarkdown } from '@/lib/markdown'
import type { Uuid } from '@/lib/transactions/types'

import { ActionCard, ActionGroupCard } from './ActionCards'
import { toolLine } from './toolLine'

/**
 * The pieces of a thread, shared by the chat and a run's record, so a card
 * looks the same wherever it appears.
 */

export { ActionCard, ActionGroupCard } from './ActionCards'

/**
 * A conversation and the box to continue it: the chat page and the Ask dialog
 * are both this. `frame` turns what was typed into what is sent, which is how
 * the Ask dialog names its row; the thread shows what was typed.
 */
export function Thread({
  status,
  conversationId,
  onThread,
  ideas,
  intro,
  placeholder,
  label = 'Message',
  frame = (typed) => typed,
  inline = false,
  autoFocus = false,
  children,
}: {
  status: AssistantStatus
  /** Null before the first question; the first send creates it. */
  conversationId: Uuid | null
  onThread: (id: Uuid) => void
  /** Questions to press, offered only before anything is asked. */
  ideas: readonly string[]
  intro?: ReactNode
  placeholder: string
  label?: string
  frame?: (typed: string) => string
  /** Inside a dialog, where the phone layout's sticky composer has no tab bar to clear. */
  inline?: boolean
  autoFocus?: boolean
  /** Between the thread and the box, where the eye goes next. */
  children?: ReactNode
}) {
  const ask = useAsk(onThread)
  const [question, setQuestion] = useState('')
  const [asked, setAsked] = useState('')
  const bottom = useRef<HTMLDivElement>(null)

  // Read from the thread rather than the mutation's result: a second question
  // has to show the first above it. Folded: a run of tool calls is one line
  // that opens.
  const conversation = useConversation(conversationId)
  const timeline = useMemo(
    () => groupToolRuns(conversationTimeline(conversation.data)),
    [conversation.data],
  )
  const writeTools = useMemo(() => writeToolNames(status.tools), [status.tools])

  // Which thing scrolls depends on the width: a pane on a desk, the document on
  // a phone (screens.css), so the pane is asked. A pane gets `scrollTop` set
  // directly, since `scrollIntoView` would drag every ancestor too.
  useEffect(() => {
    const pane = bottom.current?.closest('.chat')
    if (!pane) return
    if (getComputedStyle(pane).overflowY === 'visible') {
      bottom.current?.scrollIntoView({ block: 'end' })
      return
    }
    pane.scrollTop = pane.scrollHeight
  }, [timeline.length])

  const busy = ask.isPending
  const send = () => {
    const text = question.trim()
    if (text === '' || busy) return
    setQuestion('')
    setAsked(text)
    ask.mutate({ id: conversationId, question: frame(text) })
  }

  return (
    <>
      <div className={inline ? 'chat chat--ask' : 'chat'} aria-live="polite">
        <ThreadEntries
          entries={timeline}
          writeTools={writeTools}
          conversationId={conversationId}
        />
        {/* Said rather than left blank, so an empty pane does not read as a
            thread that failed to load. */}
        {conversationId === null && !busy ? (
          <div className="chat__empty">
            {intro}
            <div className="chips chips--wrap">
              {ideas.map((idea) => (
                <Button key={idea} variant="secondary" size="sm" onClick={() => setQuestion(idea)}>
                  {idea}
                </Button>
              ))}
            </div>
          </div>
        ) : null}
        {/* The thread is only replaced when the answer arrives; showing the
            question now makes the wait read as working rather than a failed send. */}
        {busy ? <p className="chat__asked">{asked}</p> : null}
        {busy ? (
          <p className="chat__thinking" role="status">
            <Spinner size={13} /> Working on it — reading
            the ledger
            {status.allow_writes && !status.apply_without_asking
              ? ' and drafting anything to propose'
              : ''}
            …
          </p>
        ) : null}
        <div ref={bottom} className="chat__end" />
      </div>

      {children}

      <form
        className={inline ? 'row' : 'chat__composer'}
        onSubmit={(event) => {
          event.preventDefault()
          send()
        }}
      >
        <Textarea
          aria-label={label}
          className="chat__input"
          rows={2}
          value={question}
          placeholder={placeholder}
          autoFocus={autoFocus}
          disabled={busy}
          onChange={(event) => setQuestion(event.target.value)}
          onKeyDown={(event) => {
            if (sendsOnEnter(event)) {
              event.preventDefault()
              send()
            }
          }}
        />
        <Button type="submit" variant="primary" disabled={question.trim() === '' || busy}>
          <Send size={13} aria-hidden="true" /> Send
        </Button>
      </form>
    </>
  )
}

/**
 * A whole thread, in order: turns, folded runs of tool calls, and cards (one,
 * or one for a whole bulk proposal).
 */
export function ThreadEntries({
  entries,
  writeTools,
  conversationId,
}: {
  entries: readonly GroupedEntry[]
  writeTools: Set<string>
  /** Null before the first question, when there can be no cards. */
  conversationId: Uuid | null
}) {
  return (
    <>
      {entries.map((entry) => {
        switch (entry.kind) {
          case 'tools':
            return (
              <ToolRun
                key={entry.at + entry.messages[0].id}
                messages={entry.messages}
                writeTools={writeTools}
              />
            )
          case 'message':
            return (
              <Turn key={entry.at + entry.message.id} message={entry.message} writeTools={writeTools} />
            )
          case 'group':
            return conversationId === null ? null : (
              <ActionGroupCard
                key={entry.at + entry.group}
                actions={entry.actions}
                conversationId={conversationId}
              />
            )
          case 'action':
            return conversationId === null ? null : (
              <ActionCard
                key={entry.at + entry.action.id}
                action={entry.action}
                conversationId={conversationId}
              />
            )
        }
      })}
    </>
  )
}

/** One tool argument, readably: `String()` renders a nested object as "[object Object]". */
function describeArgument(value: unknown): string {
  if (typeof value === 'object' && value !== null) return JSON.stringify(value)
  return String(value)
}

/** A run of consecutive tool calls, folded to one line that says how many it holds. */
function ToolRun({
  messages,
  writeTools,
}: {
  messages: readonly AssistantMessage[]
  writeTools: Set<string>
}) {
  const [open, setOpen] = useState(false)
  if (messages.length === 1) {
    return <Turn message={messages[0]} writeTools={writeTools} />
  }
  return (
    <div className="chat__steps">
      <button type="button" className="chat__tool chat__steps-toggle" onClick={() => setOpen(!open)}>
        {open ? <ChevronUp size={12} aria-hidden="true" /> : <ChevronDown size={12} aria-hidden="true" />}{' '}
        {plural(messages.length, 'step')}
      </button>
      {open
        ? messages.map((message) => (
            <Turn key={message.id} message={message} writeTools={writeTools} />
          ))
        : null}
    </div>
  )
}

export function Turn({
  message,
  writeTools,
}: {
  message: AssistantMessage
  writeTools: Set<string>
}) {
  if (message.role === 'tool') {
    // The audit trail, collapsed to name and arguments: the raw JSON is the
    // model's to read. The verb comes from the status's list of write tools,
    // not the name, so a change is never described as a look (`toolLine`).
    const verb = writeTools.has(message.tool_name) ? 'asked to' : 'read'
    return (
      <p className="chat__tool">
        <Wrench size={12} aria-hidden="true" /> {toolLine(verb, message.tool_name)}
        {message.tool_arguments && Object.keys(message.tool_arguments).length > 0
          ? ` (${Object.entries(message.tool_arguments)
              .map(([key, value]) => `${key}: ${describeArgument(value)}`)
              .join(', ')})`
          : ''}
      </p>
    )
  }
  // A question is one line somebody typed; an answer is Markdown the model
  // wrote, and printing it raw would leave the asterisks and fences showing.
  if (message.role === 'user') {
    return <p className="chat__asked">{message.content}</p>
  }
  return (
    <div className="chat__answered">
      <span className="chat__byline">
        <Sparkles size={12} aria-hidden="true" /> Agentifi
      </span>
      {renderMarkdown(message.content)}
    </div>
  )
}
