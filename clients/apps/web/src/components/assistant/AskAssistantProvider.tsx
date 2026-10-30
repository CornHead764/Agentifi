/**
 * "Ask" on a row, and the prompt it opens: one dialog for the whole app. A new
 * kind of row is a menu item and an `AskKind` entry.
 *
 * What travels is a reference: `questionText` names the row and the paths
 * that answer it, and the model reads them through `read_endpoint`, which
 * dispatches through the Read routes under the caller's own space. A client
 * that lied about a row's fields gains nothing, and every figure in the answer
 * traces back to the ledger.
 *
 * The exchange is a real server thread, listed in Conversations.
 */

import { MessageSquare } from 'lucide-react'
import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { useMoneyText } from '@/components/moneyText'
import { Button, Dialog, DialogActions, DialogContent } from '@/components/ui'
import { AskAssistantContext } from '@/contexts/askAssistant'
import {
  askTitle,
  kindNoun,
  questionText,
  subjectLine,
  suggestedQuestions,
  type AskSubject,
} from '@/lib/assistant/rowQuestion'
import { useAssistantStatus, type AssistantStatus } from '@/lib/clients/assistant'
import type { Uuid } from '@/lib/transactions/types'

import { Thread } from '@/pages/assistant/thread'

export function AskAssistantProvider({ children }: { children: ReactNode }) {
  const status = useAssistantStatus()
  const [subject, setSubject] = useState<AskSubject | null>(null)
  // The thread per row, kept above the dialog so reopening Ask on the same row
  // carries on rather than starting another conversation.
  const [threads, setThreads] = useState<Record<string, Uuid>>({})

  const ready = Boolean(status.data?.configured && status.data.is_enabled)
  const ask = useCallback((next: AskSubject) => setSubject(next), [])
  const value = useMemo(() => ({ ready, ask }), [ready, ask])
  const key = subject === null ? '' : subjectKey(subject)

  return (
    <AskAssistantContext value={value}>
      {children}
      {subject && status.data ? (
        <AskDialog
          subject={subject}
          status={status.data}
          thread={threads[key] ?? null}
          onThread={(id) => setThreads((current) => ({ ...current, [key]: id }))}
          onClose={() => setSubject(null)}
        />
      ) : null}
    </AskAssistantContext>
  )
}

/** One row, as a key. Not every kind has an id; every kind has a name. */
function subjectKey(subject: AskSubject): string {
  return `${subject.kind}:${subject.id ?? subject.name}`
}

/** The prompt, and the thread it turns into. `thread` comes from above so it outlives this component. */
export function AskDialog({
  subject,
  status,
  thread,
  onThread,
  onClose,
}: {
  subject: AskSubject
  status: AssistantStatus
  /** The conversation this row is already in, or null before the first send. */
  thread: Uuid | null
  onThread: (id: Uuid) => void
  onClose: () => void
}) {
  const moneyText = useMoneyText()

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        wide
        title={askTitle(subject.kind)}
        // The menu it came from is closed by now, so the row is named here.
        description={`Asking about: ${subjectLine(subject, new Date(), moneyText)}`}
        footer={
          <DialogActions
            cancel="Close"
            onCancel={onClose}
            start={
              thread === null ? null : (
                <Button asChild variant="ghost">
                  <Link to={`/assistant?conversation=${thread}`} onClick={onClose}>
                    <MessageSquare size={13} aria-hidden="true" /> Continue in Assistant
                  </Link>
                </Button>
              )
            }
          />
        }
      >
        <Thread
          inline
          autoFocus
          status={status}
          conversationId={thread}
          onThread={onThread}
          ideas={suggestedQuestions(subject.kind)}
          label="Question"
          placeholder={`What do you want to know about this ${kindNoun(subject.kind)}?`}
          frame={(typed) => questionText(subject, typed)}
        />
      </DialogContent>
    </Dialog>
  )
}
