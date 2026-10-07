/**
 * The automations tab: what is set up to run, how far it may go, and how its
 * last run ended, visible without opening anything.
 */

import { describe, expect, it } from 'vitest'

import type { AssistantStatus } from '@/lib/clients/assistant'
import {
  AUTOMATIONS_KEY,
  RUNS_KEY,
  TEMPLATES_KEY,
  describeTrigger,
  type Automation,
  type AutomationRun,
} from '@/lib/clients/automations'
import type { Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { AutomationsPanel } from './AutomationsPanel'

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
    { name: 'list_categories', description: 'The tree.', writes: false },
    { name: 'update_transaction', description: 'Propose a change.', writes: true },
  ],
}

const AUTOMATION: Automation = {
  id: '00000000-0000-0000-0000-0000000000a1' as Uuid,
  name: 'Suggest categories',
  description: '',
  is_enabled: true,
  trigger: 'transaction_arrived',
  trigger_config: { skip_transfers: true },
  filter_id: null,
  filter: null,
  prompt: 'Decide whether the category is right.',
  context: {
    transaction: true,
    similar_transactions: 12,
    categories: true,
    rules: true,
    accounts: false,
    corrections: true,
    guidance: true,
    cash_flow_history: false,
  },
  mode: 'propose',
  tools: ['list_categories', 'update_transaction'],
  model: '',
  max_tool_rounds: 6,
  confidence_threshold: 0.85,
  template_key: 'check_category',
  created_by: '00000000-0000-0000-0000-0000000000u1' as Uuid,
  created_at: '2026-09-04T10:00:00Z',
  updated_at: '2026-09-04T10:00:00Z',
  runs: 3,
  last_run_at: '2026-09-04T12:00:00Z',
  last_status: 'succeeded',
  pending_actions: 1,
}

const RUN: AutomationRun = {
  id: '00000000-0000-0000-0000-0000000000r1' as Uuid,
  automation_id: AUTOMATION.id,
  fired_by: 'transaction',
  transaction_id: '00000000-0000-0000-0000-0000000000t1' as Uuid,
  conversation_id: '00000000-0000-0000-0000-0000000000c1' as Uuid,
  dry_run: false,
  blind: false,
  expected_category_id: null,
  confidence: 0.42,
  decided_by: 'model',
  status: 'succeeded',
  subject: 'COSTCO WHSE · -140.00 · 2026-09-03',
  prompt: 'system prompt',
  output: 'Proposed Groceries.',
  error: '',
  error_code: '',
  tool_calls: 2,
  actions: 1,
  queued_at: '2026-09-04T12:00:00Z',
  started_at: '2026-09-04T12:00:01Z',
  finished_at: '2026-09-04T12:00:09Z',
}

function render(status: AssistantStatus): string {
  return renderScreen(<AutomationsPanel status={status} editing={null} onEditingChange={() => {}} />, {
    seed: [
      [AUTOMATIONS_KEY, [AUTOMATION]],
      [TEMPLATES_KEY, []],
      [[...RUNS_KEY, 50], [RUN]],
    ],
  })
}

describe('the automations tab', () => {
  it('lists each automation with its trigger, how far it may go, and its last run', () => {
    const markup = render(STATUS)
    expect(markup).toContain('Suggest categories')
    expect(markup).toContain('when a transaction arrives')
    expect(markup).toContain('proposes changes for review')
    expect(markup).toContain('3 runs')
  })

  it('warns when changes are off but an automation would make them', () => {
    // The connection's switch outranks the mode, and a run that fails at four
    // in the morning has nobody to tell — so the tab says so beforehand.
    const off = render({ ...STATUS, allow_writes: false })
    expect(off).toContain('Changes are off for the assistant')
    // The switch itself, not directions to it.
    expect(off).toContain('>Let it propose changes</button>')
    expect(off).not.toContain('/settings/assistant')
    expect(render(STATUS)).not.toContain('Changes are off for the assistant')
  })

  it('lists recent runs with what they were about, how they ended, and who decided', () => {
    const markup = render(STATUS)
    expect(markup).toContain('COSTCO WHSE')
    expect(markup).toContain('succeeded')
    expect(markup).toContain('1 change')
    expect(markup).toContain('decided by the model at 0.42')
  })
})

describe('describing a trigger', () => {
  it('reads as a sentence fragment', () => {
    expect(describeTrigger({ trigger: 'daily', trigger_config: { at: '06:30' } })).toBe('daily at 06:30')
    expect(
      describeTrigger({
        trigger: 'transaction_arrived',
        trigger_config: { skip_transfers: true },
        filter: {
          id: 'f' as Uuid,
          name: null,
          scope: 'automation',
          query_text: null,
          items: [
            {
              id: 'i' as Uuid,
              field: 'account',
              operator: 'in',
              group_index: 0,
              position: 0,
              negated: false,
              value_ids: ['a' as Uuid],
              value_texts: [],
              text: null,
              amount_min: null,
              amount_max: null,
              date_from: null,
              date_to: null,
              date_preset: null,
              state: null,
            },
          ],
        },
      }),
    ).toBe('when a transaction arrives that meets 1 condition (not a transfer)')
    expect(describeTrigger({ trigger: 'transaction_arrived', trigger_config: {}, filter: null })).toBe(
      'when a transaction arrives',
    )
    expect(describeTrigger({ trigger: 'manual', trigger_config: {} })).toBe('only when run by hand')
  })
})
