/**
 * A run's record: the question, the reply, and every tool call with its
 * arguments and result, in order.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import type { AssistantAction, AssistantStatus, Conversation } from '@/lib/clients/assistant'
import { runKey, type AutomationRun } from '@/lib/clients/automations'
import type { Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { RunDialog } from './RunDialog'

const RUN = '00000000-0000-0000-0000-0000000000r1' as Uuid
const CONVERSATION = '00000000-0000-0000-0000-0000000000c1' as Uuid

const STATUS: AssistantStatus = {
  configured: true,
  is_enabled: true,
  base_url: 'http://10.0.0.5:8099/v1',
  model: 'example-model',
  name: '',
  has_key: false,
  allow_writes: true,
  apply_without_asking: false,
  tool_call_style: 'prompted',
  tools: [
    { name: 'list_transactions', description: 'The rows.', writes: false },
    { name: 'update_transaction', description: 'Propose a change.', writes: true },
  ],
}

const ACTION: AssistantAction = {
  id: '00000000-0000-0000-0000-0000000000f1' as Uuid,
  tool: 'update_transaction',
  summary: 'File the coffee under Dining',
  method: 'PATCH',
  path: '/transactions/00000000-0000-0000-0000-0000000000t1',
  body: { category_id: '00000000-0000-0000-0000-0000000000g1' },
  status: 'pending',
  result: '',
  status_code: 0,
  created_at: '2026-09-01T10:00:02Z',
  decided_at: null,
}

const CONVERSATION_ROWS: Conversation = {
  id: CONVERSATION,
  title: 'Coffee at Corner Coffee',
  created_at: '2026-09-01T10:00:00Z',
  updated_at: '2026-09-01T10:00:03Z',
  messages: [
    {
      id: '00000000-0000-0000-0000-0000000000m1' as Uuid,
      role: 'user',
      content: 'The row: Corner Coffee, 4.50, uncategorized.',
      tool_name: '',
      tool_arguments: null,
      created_at: '2026-09-01T10:00:00Z',
    },
    {
      id: '00000000-0000-0000-0000-0000000000m2' as Uuid,
      role: 'tool',
      content: '[{"payee":"Corner Coffee","amount":"4.50"},{"payee":"Corner Coffee","amount":"5.50"}]',
      tool_name: 'list_transactions',
      tool_arguments: { payee: 'Corner Coffee', months: 3 },
      created_at: '2026-09-01T10:00:01Z',
    },
    {
      id: '00000000-0000-0000-0000-0000000000m3' as Uuid,
      role: 'tool',
      content: '{"error":"no such category"}',
      tool_name: 'update_transaction',
      tool_arguments: { category_id: 'g1' },
      created_at: '2026-09-01T10:00:01.5Z',
    },
    {
      id: '00000000-0000-0000-0000-0000000000m4' as Uuid,
      role: 'assistant',
      content: 'Two earlier coffees are filed under Dining.',
      tool_name: '',
      tool_arguments: null,
      created_at: '2026-09-01T10:00:03Z',
    },
  ],
  actions: [ACTION],
}

function run(overrides: Partial<AutomationRun> = {}): AutomationRun {
  return {
    id: RUN,
    automation_id: '00000000-0000-0000-0000-0000000000a1' as Uuid,
    fired_by: 'transaction',
    transaction_id: null,
    conversation_id: CONVERSATION,
    dry_run: false,
    blind: false,
    expected_category_id: null,
    confidence: null,
    decided_by: 'model',
    status: 'succeeded',
    subject: 'Coffee at Corner Coffee',
    prompt: 'Decide whether the category on this transaction is right.',
    output: 'It was already right.',
    error: '',
    error_code: '',
    tool_calls: 2,
    actions: 1,
    queued_at: '2026-09-01T10:00:00Z',
    started_at: '2026-09-01T10:00:00Z',
    finished_at: '2026-09-01T10:00:03Z',
    conversation: CONVERSATION_ROWS,
    ...overrides,
  }
}

function render(overrides: Partial<AutomationRun> = {}): string {
  return renderScreen(
    <RunDialog runId={RUN} status={STATUS} onClose={() => {}} />,
    { seed: [[runKey(RUN), run(overrides)]] },
  )
}

describe('a run’s record', () => {
  it('leads with what it was asked and what it answered', () => {
    const markup = render()
    expect(markup).toContain('What it was asked')
    expect(markup).toContain('Decide whether the category on this transaction is right.')
    expect(markup).toContain('What it answered')
    expect(markup).toContain('It was already right.')
  })

  it('shows each tool call with what it was called with and what came back', () => {
    const markup = render()
    expect(markup).toContain('read list transactions')
    expect(markup).toContain('&quot;payee&quot;: &quot;Corner Coffee&quot;')
    expect(markup).toContain('2 rows came back')
    // The whole answer is there too, under that line.
    expect(markup).toContain('&quot;amount&quot;: &quot;5.50&quot;')
  })

  it('does not describe a change as something it read', () => {
    const markup = render()
    expect(markup).toContain('asked to update transaction')
    expect(markup).not.toContain('read update transaction')
  })

  it('says a tool failed rather than burying it in the trace', () => {
    expect(render()).toContain('It answered with an error: no such category')
  })

  it('keeps the card the run left, in its place among the steps', () => {
    const markup = render()
    expect(markup).toContain('File the coffee under Dining')
    expect(markup.indexOf('read list transactions')).toBeLessThan(
      markup.indexOf('File the coffee under Dining'),
    )
  })

  it('reports the counts and the timing, and no figure the server does not keep', () => {
    const markup = render()
    expect(markup).toContain('Tool calls')
    expect(markup).toContain('Took')
    expect(markup).toContain('3.0s')
    expect(markup).not.toContain('Tokens')
    expect(markup).not.toContain('Cost')
  })

  it('ends on the summary rather than a dump of the whole record', () => {
    const markup = render()
    expect(markup).not.toContain('Raw run record')
    expect(markup).not.toContain('&quot;automation_id&quot;')
  })

  it('says a run answered nothing rather than leaving the section out', () => {
    // An absent section reads as a screen that failed to draw. Plenty of runs
    // ended without a closing answer, and that is the finding, not a gap.
    const markup = render({ status: 'failed', output: '', error: 'the model refused (401)' })
    expect(markup).toContain('What it answered')
    expect(markup).toContain('Nothing. It stopped before it answered.')
  })

  it('says nothing was put to a model when no prompt was recorded', () => {
    // A run settled from the household's own history never called one.
    const markup = render({ prompt: '', decided_by: 'agentifi', confidence: 0.95 })
    expect(markup).toContain('Settled without a model')
    expect(markup).toContain('decided from the history at 0.95, no model call')
  })

  it('gives a failure its reason where the eye already is', () => {
    const markup = render({ status: 'failed', error: 'the model refused (401)' })
    expect(markup).toContain('It failed')
    expect(markup).toContain('the model refused (401)')
  })

  it('numbers the steps rather than writing the order into the markup', () => {
    // The order is the point of the section, and a number in the markup would
    // be wrong the first time a step stopped being rendered.
    expect(render()).toContain('class="run-steps"')
  })
})
