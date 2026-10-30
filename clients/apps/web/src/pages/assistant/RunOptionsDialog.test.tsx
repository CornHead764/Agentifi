/**
 * Running an automation by hand: one Run action whose options reach every
 * kind of run, and an editor whose tuning knobs sit behind Advanced.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import type { AssistantStatus } from '@/lib/clients/assistant'
import { DEFAULT_RUN_OPTIONS, runRequest, type Automation } from '@/lib/clients/automations'
import type { Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { AutomationEditor } from './AutomationEditor'
import { RunOptionsDialog } from './RunOptionsDialog'

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
  tools: [{ name: 'list_categories', description: 'The tree.', writes: false }],
}

const AUTOMATION: Automation = {
  id: '00000000-0000-0000-0000-0000000000a1' as Uuid,
  name: 'Check each new transaction',
  description: '',
  is_enabled: true,
  trigger: 'transaction_arrived',
  trigger_config: {},
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
  tools: [],
  model: '',
  max_tool_rounds: 6,
  confidence_threshold: 0.85,
  template_key: 'check_category',
  created_by: '00000000-0000-0000-0000-0000000000u1' as Uuid,
  created_at: '2026-09-04T10:00:00Z',
  updated_at: '2026-09-04T10:00:00Z',
  runs: 0,
  last_run_at: '2026-09-04T12:00:00Z',
  last_status: 'succeeded',
  pending_actions: 0,
}

const DAILY: Automation = { ...AUTOMATION, trigger: 'daily' }

function wrap(node: React.ReactNode): string {
  return renderScreen(node)
}

describe('the one Run action', () => {
  it('reaches every run option this dialog offers', () => {
    const id = AUTOMATION.id
    const dry = DEFAULT_RUN_OPTIONS
    const real = { ...dry, dryRun: false }
    expect(runRequest(AUTOMATION, dry)).toEqual({ id, recent: undefined, dry_run: true, blind: false })
    expect(runRequest(AUTOMATION, { ...dry, recent: 5 })).toEqual({
      id,
      recent: 5,
      dry_run: true,
      blind: false,
    })
    expect(runRequest(AUTOMATION, { ...dry, blind: true })).toEqual({
      id,
      recent: undefined,
      dry_run: true,
      blind: true,
    })
    expect(runRequest(AUTOMATION, real)).toEqual({ id, recent: undefined, dry_run: false, blind: false })
    expect(runRequest(AUTOMATION, { ...real, recent: 5 })).toEqual({
      id,
      recent: 5,
      dry_run: false,
      blind: false,
    })
  })

  it('never sends a blind run for real, nor a row count to a trigger without rows', () => {
    expect(runRequest(AUTOMATION, { dryRun: false, recent: 1, blind: true }).blind).toBe(false)
    expect(runRequest(DAILY, { dryRun: true, recent: 5, blind: true })).toEqual({
      id: DAILY.id,
      recent: undefined,
      dry_run: true,
      blind: false,
    })
  })

  it('starts as a dry run, and offers row counts and blind only to a transaction trigger', () => {
    const perRow = wrap(<RunOptionsDialog automation={AUTOMATION} onRun={() => {}} onClose={() => {}} />)
    expect(perRow).toContain('Run Check each new transaction')
    expect(perRow).toContain('the 5 most recent matching transactions')
    expect(perRow).toContain('blind')
    expect(perRow).toContain('>Dry run</button>')

    const daily = wrap(<RunOptionsDialog automation={DAILY} onRun={() => {}} onClose={() => {}} />)
    expect(daily).not.toContain('most recent matching')
    expect(daily).not.toContain('blind')
  })
})

describe('the automation editor', () => {
  it('keeps what it is told and how far it may go in view, and the tuning behind Advanced', () => {
    const markup = wrap(
      <AutomationEditor
        seed={{ automation: AUTOMATION }}
        status={STATUS}
        onClose={() => {}}
        onOpenRun={() => {}}
      />,
    )
    const fold = markup.indexOf('<summary>Advanced</summary>')
    expect(fold).toBeGreaterThan(-1)
    expect(markup.indexOf('Instructions')).toBeLessThan(fold)
    expect(markup.indexOf('How far it may go')).toBeLessThan(fold)
    for (const knob of ['Hand it up front', 'Act from the history alone', 'Tools it may use', 'Tool rounds']) {
      expect(markup.indexOf(knob)).toBeGreaterThan(fold)
    }
    expect(markup).not.toContain('review queue')
  })
})
