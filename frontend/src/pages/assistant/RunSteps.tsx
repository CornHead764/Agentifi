import { Pencil, Wrench } from 'lucide-react'

import { renderMarkdown } from '@/lib/markdown'
import type { TimelineEntry } from '@/lib/assistant/history'
import type { AssistantMessage } from '@/lib/clients/assistant'
import type { Uuid } from '@/lib/transactions/types'

import { ActionCard } from './thread'
import { describeToolResult, prettyJson } from './runView'
import { toolLine } from './toolLine'

/**
 * What a run did, step by step. The chat hides a tool's answer; a run was
 * unattended, so its record carries each call's arguments and result, in
 * order. The answer is folded, summarized by its shape (a count, or the error)
 * so a long trace is still one screen.
 */
export function RunSteps({
  entries,
  writeTools,
  conversationId,
  onDecided,
}: {
  entries: readonly TimelineEntry[]
  writeTools: Set<string>
  conversationId: Uuid
  onDecided?: () => void
}) {
  return (
    <ol className="run-steps">
      {entries.map((entry) =>
        entry.kind === 'action' ? (
          <li key={entry.at + entry.action.id} className="run-steps__step">
            <p className="run-steps__head">
              <Pencil size={12} aria-hidden="true" /> proposed a change
            </p>
            <ActionCard
              action={entry.action}
              conversationId={conversationId}
              onDecided={onDecided}
            />
          </li>
        ) : (
          <li key={entry.at + entry.message.id} className="run-steps__step">
            <MessageStep message={entry.message} writeTools={writeTools} />
          </li>
        ),
      )}
    </ol>
  )
}

function MessageStep({
  message,
  writeTools,
}: {
  message: AssistantMessage
  writeTools: Set<string>
}) {
  if (message.role === 'tool') {
    // The verb comes from the catalogue, never from the name: a tool that
    // creates something must not be introduced as something that was read.
    const verb = writeTools.has(message.tool_name) ? 'asked to' : 'read'
    const args = message.tool_arguments
    return (
      <>
        <p className="run-steps__head">
          <Wrench size={12} aria-hidden="true" /> {toolLine(verb, message.tool_name)}
        </p>
        {args !== null && Object.keys(args).length > 0 ? (
          <>
            <p className="eyebrow">With</p>
            <pre className="run-steps__json">{prettyJson(args)}</pre>
          </>
        ) : (
          <p className="eyebrow">With no arguments</p>
        )}
        <details className="run-detail__fold">
          <summary>{describeToolResult(message.content)}</summary>
          <pre className="run-steps__json">{prettyJson(message.content)}</pre>
        </details>
      </>
    )
  }
  // The opening message is the facts the automation handed the model: as long
  // as the prompt and not written by anybody, so it stays folded.
  if (message.role === 'user') {
    return (
      <details className="run-detail__fold">
        <summary>The facts it was handed</summary>
        <pre className="automation-preview__text">{message.content}</pre>
      </details>
    )
  }
  return (
    <>
      <p className="run-steps__head">it said</p>
      <div className="chat__answered">{renderMarkdown(message.content)}</div>
    </>
  )
}
