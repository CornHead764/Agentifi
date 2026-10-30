import { describe, expect, it } from 'vitest'

import type { AutomationRun } from '@/lib/clients/automations'
import type { Uuid } from '@/lib/transactions/types'

import {
  describeToolResult,
  missingPrompt,
  missingReply,
  prettyJson,
  runDuration,
  runFacts,
  runFix,
  runOutcome,
} from './runView'

function run(overrides: Partial<AutomationRun> = {}): AutomationRun {
  return {
    id: '00000000-0000-0000-0000-0000000000r1' as Uuid,
    automation_id: '00000000-0000-0000-0000-0000000000a1' as Uuid,
    fired_by: 'transaction',
    transaction_id: null,
    conversation_id: null,
    dry_run: false,
    blind: false,
    expected_category_id: null,
    confidence: null,
    decided_by: '',
    status: 'succeeded',
    subject: 'Coffee at Corner Coffee',
    prompt: 'Decide whether the category is right.',
    output: 'The category was already right.',
    error: '',
    error_code: '',
    tool_calls: 2,
    actions: 1,
    queued_at: '2026-09-01T10:00:00Z',
    started_at: '2026-09-01T10:00:01Z',
    finished_at: '2026-09-01T10:00:02.5Z',
    ...overrides,
  }
}

describe('how long a run took', () => {
  it('measures from the moment the worker picked it up', () => {
    expect(runDuration(run())).toBe('1.5s')
  })

  it('says nothing at all while it is still going', () => {
    // "0s" would read as a run that did nothing, which is a different claim.
    expect(runDuration(run({ finished_at: null }))).toBeNull()
    expect(runDuration(run({ started_at: null, finished_at: null }))).toBeNull()
  })

  it('stays readable at both ends of the scale', () => {
    expect(
      runDuration(run({ started_at: '2026-09-01T10:00:00Z', finished_at: '2026-09-01T10:00:00.4Z' })),
    ).toBe('400 ms')
    expect(
      runDuration(run({ started_at: '2026-09-01T10:00:00Z', finished_at: '2026-09-01T10:02:05Z' })),
    ).toBe('2m 05s')
  })
})

describe('what became of a run', () => {
  it('carries the reason a failure failed', () => {
    const outcome = runOutcome(run({ status: 'failed', error: 'the model refused (401)' }))
    expect(outcome.headline).toBe('It failed')
    expect(outcome.detail).toBe('the model refused (401)')
  })

  it('says so when a failure recorded no reason, rather than repeating the word', () => {
    expect(runOutcome(run({ status: 'failed' })).detail).toBe('Nothing was recorded about why.')
  })

  it('distinguishes one still in flight from one that ended', () => {
    expect(runOutcome(run({ status: 'queued' })).headline).toBe('It has not started')
    expect(runOutcome(run({ status: 'running' })).headline).toBe('It is still running')
    expect(runOutcome(run()).headline).toBe('It finished')
  })
})

describe('a run with no prompt recorded', () => {
  // Two opposite meanings behind the same empty section: one was settled
  // without a model, the other has not started.
  it('says which of the two it is', () => {
    expect(missingPrompt(run())).toContain('Settled without a model')
    expect(missingPrompt(run({ status: 'queued' }))).toBe(
      'Nothing yet. The prompt is recorded when the run starts.',
    )
  })
})

describe('a run that never answered', () => {
    // There are fewer answers stored than questions asked, and an empty section
    // reads as a screen that failed to draw.
  it('says so, and says which kind of nothing it is', () => {
    expect(missingReply(run({ status: 'failed' }))).toBe('Nothing. It stopped before it answered.')
    expect(missingReply(run({ status: 'skipped' }))).toBe('Nothing. No model was called.')
    expect(missingReply(run({ status: 'running' }))).toBe('Nothing yet.')
    expect(missingReply(run())).toContain('finished without a closing answer')
  })
})

describe('a run’s mechanical facts', () => {
  it('reports the counts and the timing the run actually recorded', () => {
    expect(runFacts(run())).toEqual([
      { label: 'Tool calls', value: '2' },
      { label: 'Changes proposed', value: '1' },
      { label: 'Took', value: '1.5s' },
    ])
  })

    // Nothing is shown that is not stored: a zero token count, cost or model
    // beside somebody's money would be a lie.
  it('invents no tokens, no cost and no model', () => {
    const labels = runFacts(run()).map((fact) => fact.label)
    expect(labels).not.toContain('Tokens')
    expect(labels).not.toContain('Cost')
    expect(labels).not.toContain('Model')
  })

  it('says what a dry run’s changes were', () => {
    expect(runFacts(run({ dry_run: true })).map((fact) => fact.label)).toContain(
      'Changes it would have made',
    )
  })

  it('names who decided it when anybody did, and leaves the row out when nobody has', () => {
    expect(runFacts(run({ decided_by: 'agentifi', confidence: 0.92 }))).toContainEqual({
      label: 'Decided',
      value: 'decided from the history at 0.92, no model call',
    })
    expect(runFacts(run()).map((fact) => fact.label)).not.toContain('Decided')
  })

  it('leaves out the timing rather than guessing at it while a run is going', () => {
    expect(runFacts(run({ status: 'running', finished_at: null })).map((one) => one.label)).not.toContain(
      'Took',
    )
  })
})

describe('JSON as a person reads it', () => {
  it('indents what parses', () => {
    expect(prettyJson('{"amount":"12.50"}')).toBe('{\n  "amount": "12.50"\n}')
    expect(prettyJson({ amount: '12.50' })).toBe('{\n  "amount": "12.50"\n}')
  })

  it('hands back untouched what does not', () => {
    // A provider that answered with an error page is a thing to see.
    expect(prettyJson('<html>502 Bad Gateway</html>')).toBe('<html>502 Bad Gateway</html>')
    expect(prettyJson('')).toBe('')
  })
})

describe('a tool’s answer in one line', () => {
  it('counts a list, because how many came back is the question', () => {
    expect(describeToolResult('[{"id":"1"},{"id":"2"}]')).toBe('2 rows came back')
    expect(describeToolResult('[{"id":"1"}]')).toBe('1 row came back')
  })

  it('counts the list inside the wrapper an endpoint put it in', () => {
    // Counting the object's own fields would report "1 field" about two
    // hundred transactions.
    expect(describeToolResult('{"items":[1,2,3]}')).toBe('3 rows came back')
    expect(describeToolResult('{"transactions":[]}')).toBe('0 rows came back')
  })

  it('quotes an error, because a tool that failed is why the answer is wrong', () => {
    expect(describeToolResult('{"error":"no such account"}')).toBe(
      'It answered with an error: no such account',
    )
  })

  it('falls back to the shape it can describe', () => {
    expect(describeToolResult('{"balance":"10.00","as_of":"2026-09-01"}')).toBe(
      '2 fields came back',
    )
    expect(describeToolResult('"nothing to do"')).toBe('nothing to do')
    expect(describeToolResult('   ')).toBe('Nothing was recorded')
    expect(describeToolResult('not json at all\nsecond line')).toBe('not json at all')
  })

  it('trims a long line with the whole of it still to hand, and no ellipsis', () => {
    const summary = describeToolResult('x'.repeat(400))
    expect(summary).toContain('(more below)')
    expect(summary).not.toContain('…')
    expect(summary.length).toBeLessThan(150)
  })
})

describe('the fix a failed run offers', () => {
  const ready = { configured: true, is_enabled: true, allow_writes: false }

  it('offers the changes switch for a run refused with changes off, while they still are', () => {
    const refused = run({ status: 'failed', error_code: 'changes_off' })
    expect(runFix(refused, ready)).toBe('turn_on_changes')
    expect(runFix(refused, { ...ready, allow_writes: true })).toBeNull()
  })

  it('offers the setup for a run with no model to ask, until there is one', () => {
    const refused = run({ status: 'failed', error_code: 'assistant_unavailable' })
    expect(runFix(refused, { ...ready, is_enabled: false })).toBe('set_up_assistant')
    expect(runFix(refused, { ...ready, configured: false })).toBe('set_up_assistant')
    expect(runFix(refused, ready)).toBeNull()
  })

  it('offers nothing for any other failure, or for a run that did not fail', () => {
    expect(runFix(run({ status: 'failed', error_code: '' }), ready)).toBeNull()
    expect(runFix(run({ error_code: 'changes_off' }), ready)).toBeNull()
  })
})
