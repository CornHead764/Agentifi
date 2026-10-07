/**
 * A run, in the words and shapes the dialog reads it with. Nothing is
 * invented: every string comes off a field the server records, which is why
 * there is no token count, no cost and no model name.
 */

import type { AssistantStatus } from '@/lib/clients/assistant'
import type { AutomationRun } from '@/lib/clients/automations'
import { describeDecision } from '@/lib/clients/automations'
import { plural } from '@/lib/format'
import { isPlainObject } from '@/lib/typeGuards'

/** One mechanical fact: its label, and what this run says for it. */
export interface RunFact {
  label: string
  value: string
}

/** How long it took from pickup; null until it has both started and finished, since "0s" reads as a run that did nothing. */
export function runDuration(
  run: Pick<AutomationRun, 'started_at' | 'finished_at'>,
): string | null {
  if (run.started_at === null || run.finished_at === null) return null
  const ms = Date.parse(run.finished_at) - Date.parse(run.started_at)
  if (!Number.isFinite(ms) || ms < 0) return null
  if (ms < 1000) return `${ms} ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`
  const seconds = Math.round(ms / 1000)
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, '0')}s`
}

/**
 * What became of it, as the sentence beside the status badge. A failure with
 * nothing recorded says so, since the empty explanation is itself the finding.
 */
export function runOutcome(
  run: Pick<AutomationRun, 'status' | 'error'>,
): { headline: string; detail: string } {
  const why = run.error.trim()
  switch (run.status) {
    case 'failed':
      return {
        headline: 'It failed',
        detail: why === '' ? 'Nothing was recorded about why.' : why,
      }
    case 'skipped':
      return {
        headline: 'It was skipped',
        detail: why === '' ? 'Nothing was called and nothing was changed.' : why,
      }
    case 'queued':
      return { headline: 'It has not started', detail: 'Waiting for the worker to reach it.' }
    case 'running':
      return { headline: 'It is still running', detail: 'What is below is what it has done so far.' }
    default:
      return { headline: 'It finished', detail: why }
  }
}

/**
 * The one control that would have let a failed run act, read from the run's
 * code and checked against the assistant now: a run that failed with changes
 * off offers the switch only while they are still off.
 */
export function runFix(
  run: Pick<AutomationRun, 'status' | 'error_code'>,
  status: Pick<AssistantStatus, 'configured' | 'is_enabled' | 'allow_writes'>,
): 'set_up_assistant' | 'turn_on_changes' | null {
  if (run.status !== 'failed') return null
  if (run.error_code === 'assistant_unavailable' && !(status.configured && status.is_enabled)) {
    return 'set_up_assistant'
  }
  if (run.error_code === 'changes_off' && !status.allow_writes) return 'turn_on_changes'
  return null
}

/**
 * What stands in for a prompt that was never recorded. A run settled from the
 * household's history never had one, and one not yet started has not written
 * it; the two look alike and mean opposite things.
 */
export function missingPrompt(run: Pick<AutomationRun, 'status'>): string {
  if (run.status === 'queued') return 'Nothing yet. The prompt is recorded when the run starts.'
  return 'None. Settled without a model.'
}

/** What stands in for a reply that never came, which says which kind of nothing it is. */
export function missingReply(run: Pick<AutomationRun, 'status'>): string {
  switch (run.status) {
    case 'failed':
      return 'Nothing. It stopped before it answered.'
    case 'skipped':
      return 'Nothing. No model was called.'
    case 'queued':
    case 'running':
      return 'Nothing yet.'
    default:
      return 'Nothing. It finished without a closing answer; the steps below are all it recorded.'
  }
}

/** The counts and timings, each omitted rather than zeroed when the run does not carry it. */
export function runFacts(run: AutomationRun): RunFact[] {
  const facts: RunFact[] = [
    { label: 'Tool calls', value: String(run.tool_calls) },
    {
      label: run.dry_run ? 'Changes it would have made' : 'Changes proposed',
      value: String(run.actions),
    },
  ]
  const took = runDuration(run)
  if (took !== null) facts.push({ label: 'Took', value: took })
  const decision = describeDecision(run)
  if (decision !== '') facts.push({ label: 'Decided', value: decision })
  return facts
}

/** JSON as a person reads it. Anything that will not parse is returned untouched. */
export function prettyJson(value: unknown): string {
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value.trim()), null, 2)
    } catch {
      return value
    }
  }
  return JSON.stringify(value, null, 2) ?? String(value)
}

const WRAPPERS = ['items', 'rows', 'results', 'data', 'transactions'] as const

/**
 * A tool's answer in one line. A list says its length, including one the
 * endpoint wrapped in an object (counting its fields would say "1 field"); an
 * error is quoted.
 */
export function describeToolResult(result: string): string {
  const text = result.trim()
  if (text === '') return 'Nothing was recorded'
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return clip(text.split('\n')[0])
  }
  if (Array.isArray(parsed)) return countLine(parsed.length)
  if (isPlainObject(parsed)) {
    if (typeof parsed.error === 'string' && parsed.error !== '') {
      return `It answered with an error: ${clip(parsed.error)}`
    }
    for (const key of WRAPPERS) {
      const wrapped = parsed[key]
      if (Array.isArray(wrapped)) return countLine(wrapped.length)
    }
    const count = Object.keys(parsed).length
    return `${plural(count, 'field')} came back`
  }
  return clip(String(parsed))
}

function countLine(count: number): string {
  return `${plural(count, 'row')} came back`
}

function clip(text: string, limit = 120): string {
  const trimmed = text.trim()
  // No ellipsis: this app does not truncate a figure behind three dots. The
  // whole of it is one click away in the fold this line is the summary of.
  return trimmed.length <= limit ? trimmed : `${trimmed.slice(0, limit)} (more below)`
}
