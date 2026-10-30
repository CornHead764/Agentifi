import { useMemo, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'

import { SuggestionSummary } from '@/components/transactions/SuggestionCard'
import { Badge, Button, Callout, Dialog, DialogContent, SkeletonRows } from '@/components/ui'
import { conversationTimeline } from '@/lib/assistant/history'
import { runDecision, type RunDecision } from '@/lib/assistant/runDecision'
import { writeToolNames, type AssistantStatus } from '@/lib/clients/assistant'
import {
  AUTOMATIONS_KEY,
  RUN_TONE,
  describeFiredBy,
  useAutomationRun,
} from '@/lib/clients/automations'
import { renderMarkdown } from '@/lib/markdown'
import { basisLabel } from '@/lib/transactions/suggestions'
import type { Uuid } from '@/lib/transactions/types'

import { AssistantSetupDialog, TurnOnChangesButton } from './connection'
import { RunSteps } from './RunSteps'
import { missingPrompt, missingReply, runFacts, runFix, runOutcome } from './runView'
import { timeAgo } from '@agentifi/core/format'

/**
 * One run, in full: what it was asked, what it answered, what it did on the
 * way, and what became of it.
 *
 * Nothing here is computed from anything the server does not record: no token
 * count, cost or model line (see `runView.ts`).
 */
export function RunDialog({
  runId,
  status,
  onClose,
}: {
  runId: Uuid | null
    /** Null while the catalogue has not been fetched. It is read only for which
     *  tools write; a missing one makes every line a plain one, since calling a
     *  change tool "read" is worse than saying neither. */
  status: AssistantStatus | null
  onClose: () => void
}) {
  const run = useAutomationRun(runId)
  const client = useQueryClient()
  // Deciding a card here invalidates the conversation, which this dialog does
  // not read; without this the card stays pending until reopened.
  const refresh = () => void client.invalidateQueries({ queryKey: AUTOMATIONS_KEY })
  // The closing message is the reply, which the section above already shows;
  // when the thread ends on it verbatim, the steps stop one turn earlier.
  const timeline = useMemo(() => {
    const entries = conversationTimeline(run.data?.conversation)
    const last = entries.at(-1)
    if (
      last?.kind === 'message' &&
      last.message.role === 'assistant' &&
      last.message.content.trim() === (run.data?.output ?? '').trim() &&
      last.message.content.trim() !== ''
    ) {
      return entries.slice(0, -1)
    }
    return entries
  }, [run.data])
  const writeTools = useMemo(() => writeToolNames(status?.tools), [status])
  const outcome = run.data ? runOutcome(run.data) : null
  const decision = run.data ? runDecision(run.data) : null
  const evidence = run.data?.evidence ?? []
  const fix = run.data && status ? runFix(run.data, status) : null
  const [settingUp, setSettingUp] = useState(false)

  return (
    <Dialog open={runId !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        wide
        title={run.data ? run.data.subject || 'Run' : 'Run'}
        description={
          run.data
            ? `${describeFiredBy(run.data.fired_by)} · queued ${timeAgo(run.data.queued_at, 'long')}${
                run.data.finished_at ? ` · finished ${timeAgo(run.data.finished_at, 'long')}` : ''
              }`
            : undefined
        }
      >
        {run.data && outcome ? (
          <div className="stack run-detail">
            <p className="run-detail__status">
              <Badge tone={RUN_TONE[run.data.status]}>{run.data.status}</Badge>
              <span className="muted">{outcome.headline}</span>
            </p>

            {/* Why it failed reads as a warning; anything else it has to say
                about itself is a line under the badge. */}
            {outcome.detail !== '' ? (
              run.data.status === 'failed' ? (
                <Callout
                  tone="warning"
                  actions={
                    fix === 'turn_on_changes' && status ? (
                      <TurnOnChangesButton status={status} />
                    ) : fix === 'set_up_assistant' ? (
                      <Button variant="secondary" size="sm" onClick={() => setSettingUp(true)}>
                        {status?.configured ? 'Turn the assistant on' : 'Set up the assistant'}
                      </Button>
                    ) : null
                  }
                >
                  {outcome.detail}
                </Callout>
              ) : (
                <p className="muted">{outcome.detail}</p>
              )
            ) : null}

            {decision && run.data ? (
              <section className="run-decision">
                <p className="run-decision__status">
                  <Badge tone={DECISION_TONE[decision.status]}>
                    {DECISION_WORDS[decision.status]}
                  </Badge>
                </p>
                <SuggestionSummary
                  split={decision.split}
                  lines={decision.lines}
                  confidence={run.data.confidence}
                  categoryName={(id) => decisionCategoryName(decision, id)}
                  basis={basisLabel({
                    basis: decision.basis,
                    decided_by: run.data.decided_by,
                    run_id: run.data.id,
                  })}
                  sentence={decision.sentence}
                />
              </section>
            ) : null}

            {evidence.length > 0 ? (
              <section>
                <h3>What it saw</h3>
                <ul className="run-evidence">
                  {evidence.map((line) => (
                    <li key={line}>{line}</li>
                  ))}
                </ul>
              </section>
            ) : null}

            <dl className="run-facts">
              {runFacts(run.data).map((fact) => (
                <div key={fact.label} className="run-facts__fact">
                  <dt className="eyebrow">{fact.label}</dt>
                  <dd>{fact.value}</dd>
                </div>
              ))}
            </dl>

            {run.data.blind ? (
              <Callout>
                Blind dry run: category hidden from it. Cards show what it would have chosen.
                Nothing changed.
              </Callout>
            ) : run.data.dry_run ? (
              <Callout>Dry run: cards show what it would have done. Nothing changed.</Callout>
            ) : null}

            <details className="run-detail__fold">
              <summary>Full exchange: what it was asked and what it answered</summary>
              <div className="stack">
                <section>
                  <h3>What it was asked</h3>
                  {run.data.prompt.trim() === '' ? (
                    <p className="muted">{missingPrompt(run.data)}</p>
                  ) : (
                    <pre className="automation-preview__text">{run.data.prompt}</pre>
                  )}
                </section>

                {/* Both halves render whatever the run holds, including nothing: an
                    absent section reads as a screen that failed to draw. */}
                <section>
                  <h3>What it answered</h3>
                  {run.data.output.trim() === '' ? (
                    <p className="muted">{missingReply(run.data)}</p>
                  ) : (
                    <div className="chat__answered run-detail__output">
                      {renderMarkdown(run.data.output)}
                    </div>
                  )}
                </section>
              </div>
            </details>

            {run.data.conversation ? (
              <section>
                <h3>What it did</h3>
                <RunSteps
                  entries={timeline}
                  writeTools={writeTools}
                  conversationId={run.data.conversation.id}
                  onDecided={refresh}
                />
              </section>
            ) : null}
          </div>
        ) : (
          <SkeletonRows rows={4} />
        )}
        <AssistantSetupDialog
          open={settingUp}
          onClose={() => setSettingUp(false)}
        />
      </DialogContent>
    </Dialog>
  )
}

const DECISION_WORDS: Record<RunDecision['status'], string> = {
  pending: 'Waiting for you',
  applying: 'Applying',
  applied: 'Applied',
  failed: 'Did not apply',
  discarded: 'Discarded',
  simulated: 'Dry run only',
}

const DECISION_TONE: Record<RunDecision['status'], 'neutral' | 'accent' | 'income' | 'expense'> = {
  pending: 'accent',
  applying: 'accent',
  applied: 'income',
  failed: 'expense',
  discarded: 'neutral',
  simulated: 'neutral',
}

function decisionCategoryName(decision: RunDecision, id: Uuid | null): string {
  if (id === null) return 'Uncategorized'
  return decision.names[id] ?? 'A category since removed'
}
