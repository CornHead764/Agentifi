import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { plural } from '@/lib/format'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { fromFilterItems } from '@/lib/reports/savedFilter'
import {
  EMPTY_DRAFT,
  toFilterItems,
  type FilterDraft,
  type FilterUniverse,
} from '@/lib/transactions/filter'
import type { FilterItemWrite, FilterRead, Uuid } from '@/lib/transactions/types'

import { registerParams, type RegisterQuery } from '@/lib/transactions/api'

import { CONVERSATIONS_KEY, type AssistantAction, type Conversation } from './assistant'
import { SUGGESTION_BATCH_KEY } from './suggestionBatches'

export type AutomationTrigger = 'transaction_arrived' | 'daily' | 'manual'
export type AutomationMode = 'observe' | 'propose' | 'apply'
export type RunStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'skipped'

/** Which rows a transaction trigger fires on is the automation's `filter`, not this. */
export interface TriggerConfig {
  /** "HH:MM" in the space's zone. */
  at?: string
  /** Also skips a matched transfer pair, which no filter item reads. */
  skip_transfers?: boolean
}

export interface AutomationContext {
  transaction: boolean
  similar_transactions: number
  categories: boolean
  rules: boolean
  accounts: boolean
  /** Categories the household changed on earlier proposals, this payee's first. */
  corrections: boolean
  /** The guidance notes whose conditions select this row, or all on a run about no row. */
  guidance: boolean
  /** Twelve months of dates and amounts only; payees deliberately withheld. */
  cash_flow_history: boolean
}

export interface Automation {
  id: Uuid
  name: string
  description: string
  is_enabled: boolean
  trigger: AutomationTrigger
  trigger_config: TriggerConfig
  /** Both null is every row. */
  filter_id: Uuid | null
  filter: FilterRead | null
  prompt: string
  context: AutomationContext
  mode: AutomationMode
  /** Empty is every tool the mode allows. */
  tools: string[]
  /** Blank for the connection's model. */
  model: string
  max_tool_rounds: number
  /**
   * The history-vote score at or above which the payee's past categorizations
   * decide without the model. Zero always asks.
   */
  confidence_threshold: number
  /** The built-in this one was started from, blank for one written from scratch. */
  template_key: string
  created_by: Uuid
  created_at: string
  updated_at: string
  runs: number
  last_run_at: string | null
  last_status: string
  pending_actions: number
}

/**
 * A patch sends only what changed. Absent `conditions` leaves the stored
 * filter alone; an empty list drops it.
 */
export type AutomationForm = { conditions?: FilterItemWrite[] } & Partial<
  Pick<
    Automation,
    | 'name'
    | 'description'
    | 'is_enabled'
    | 'trigger'
    | 'trigger_config'
    | 'prompt'
    | 'context'
    | 'mode'
    | 'tools'
    | 'model'
    | 'max_tool_rounds'
    | 'confidence_threshold'
  >
>

export interface AutomationTemplate {
  key: string
  name: string
  description: string
  trigger: AutomationTrigger
  trigger_config: TriggerConfig
  prompt: string
  context: AutomationContext
  mode: AutomationMode
  tools: string[]
  confidence_threshold: number
}

export interface AutomationRun {
  id: Uuid
  automation_id: Uuid
  fired_by: 'transaction' | 'schedule' | 'manual' | 'match'
  transaction_id: Uuid | null
  conversation_id: Uuid | null
  dry_run: boolean
  /** Shown the row with its category hidden; `expected_category_id` is what the row said. */
  blind: boolean
  expected_category_id: Uuid | null
  /** Null when no history vote was taken. */
  confidence: number | null
  /** `agentifi` is the history vote alone; '' is nobody yet. */
  decided_by: 'agentifi' | 'model' | ''
  status: RunStatus
  subject: string
  /** The system prompt exactly as sent. */
  prompt: string
  output: string
  error: string
  /** A failure one control fixes; '' otherwise. */
  error_code: '' | 'assistant_unavailable' | 'changes_off'
  tool_calls: number
  actions: number
  queued_at: string
  started_at: string | null
  finished_at: string | null
  /** Detail endpoint only. */
  conversation?: Conversation
}

export interface AutomationPreview {
  prompt: string
  opening: string
  tools: string[]
  transaction_id: Uuid | null
}

export interface PendingAutomationAction extends AssistantAction {
  /** A decision on the card is made against this thread. */
  conversation_id: Uuid
  run_id: Uuid
  automation_id: Uuid
  subject: string
}

export const AUTOMATIONS_KEY = ['assistant', 'automations'] as const
export const TEMPLATES_KEY = ['assistant', 'automations', 'templates'] as const
export const PENDING_KEY = ['assistant', 'automations', 'pending'] as const
export const RUNS_KEY = ['assistant', 'automations', 'runs'] as const
export function runKey(id: Uuid | null) {
  return ['assistant', 'automations', 'runs', id ?? 'none'] as const
}

export function useAutomations() {
  return useQuery({
    queryKey: AUTOMATIONS_KEY,
    queryFn: ({ signal }) => api.get<Automation[]>('/assistant-automations', undefined, signal),
  })
}

export function useAutomationTemplates() {
  return useQuery({
    queryKey: TEMPLATES_KEY,
    queryFn: ({ signal }) =>
      api.get<AutomationTemplate[]>('/assistant-automations/templates', undefined, signal),
    staleTime: Infinity,
  })
}

export function usePendingAutomationActions() {
  return useQuery({
    queryKey: PENDING_KEY,
    queryFn: ({ signal }) =>
      api.get<PendingAutomationAction[]>('/assistant-automations/pending', undefined, signal),
  })
}

export function useAutomationRuns(limit = 50) {
  return useQuery({
    queryKey: [...RUNS_KEY, limit],
    queryFn: ({ signal }) =>
      api.get<AutomationRun[]>(`/assistant-automations/runs?limit=${limit}`, undefined, signal),
    refetchInterval: (query) =>
      query.state.data?.some((run) => run.status === 'queued' || run.status === 'running')
        ? 4000
        : false,
  })
}

export function useAutomationRun(id: Uuid | null) {
  return useQuery({
    queryKey: runKey(id),
    queryFn: ({ signal }) =>
      api.get<AutomationRun>(`/assistant-automations/runs/${id}`, undefined, signal),
    enabled: id !== null,
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status === 'queued' || status === 'running' ? 3000 : false
    },
  })
}

export function useCreateAutomation() {
  return useInvalidatingMutation(
    (form: AutomationForm) => api.post<Automation>('/assistant-automations', form),
    [AUTOMATIONS_KEY],
  )
}

export function useUpdateAutomation() {
  return useInvalidatingMutation(
    ({ id, ...form }: AutomationForm & { id: Uuid }) =>
      api.patch<Automation>(`/assistant-automations/${id}`, form),
    [AUTOMATIONS_KEY],
  )
}

export function useDeleteAutomation() {
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/assistant-automations/${id}`),
    [AUTOMATIONS_KEY],
    { failure: 'That automation was not deleted' },
  )
}

export interface RunOptions {
  dryRun: boolean
  /** Transaction triggers only: how many of the latest matching rows. */
  recent: 1 | 5
  blind: boolean
}

export const DEFAULT_RUN_OPTIONS: RunOptions = { dryRun: true, recent: 1, blind: false }

export function runRequest(automation: Pick<Automation, 'id' | 'trigger'>, options: RunOptions) {
  const perRow = automation.trigger === 'transaction_arrived'
  return {
    id: automation.id,
    recent: perRow && options.recent > 1 ? options.recent : undefined,
    dry_run: options.dryRun,
    // A blind run is only ever a dry run: it measures the model, not the ledger.
    blind: perRow && options.dryRun && options.blind,
  }
}

/**
 * About one row the finished run comes back; with `recent` the runs are
 * queued and an array returns.
 */
export function useRunAutomation() {
  const client = useQueryClient()
  return useInvalidatingMutation(
    ({
      id,
      transaction_id,
      recent,
      dry_run,
      blind,
    }: {
      id: Uuid
      transaction_id?: Uuid
      recent?: number
      /** Works with changes switched off. */
      dry_run?: boolean
      /** A dry run with the row's category hidden. */
      blind?: boolean
    }) =>
      api.post<AutomationRun | AutomationRun[]>(`/assistant-automations/${id}/run`, {
        transaction_id,
        recent,
        dry_run,
        blind,
      }),
    [AUTOMATIONS_KEY, CONVERSATIONS_KEY],
    {
      onSuccess: (result) => {
        if (!Array.isArray(result)) client.setQueryData(runKey(result.id), result)
      },
    },
  )
}

/** `batch_id` is the request to follow, null when nothing was queued. */
export interface FiredRuns {
  rows: number
  queued: number
  batch_id: Uuid | null
}

/** Named rows, or every row the register's query matches, sent as "Mark all as reviewed" sends it. */
export type FireTarget = { ids: Uuid[] } | { query: RegisterQuery }

/** Fires the transaction automations as if the rows had just arrived; queued, not awaited. */
export function useFireAutomations() {
  return useInvalidatingMutation(
    ({ target, force }: { target: FireTarget; force?: boolean }) =>
      'ids' in target
        ? api.post<FiredRuns>('/assistant-automations/fire', { transaction_ids: target.ids, force })
        : api.post<FiredRuns>(`/assistant-automations/fire?${registerParams(target.query)}`, {
            all_matching: true,
            force,
          }),
    [AUTOMATIONS_KEY, SUGGESTION_BATCH_KEY],
    // `useSuggestCategories` says why, with the step that fixes it.
    { failure: false },
  )
}

export function usePreviewAutomation() {
  return useMutation({
    mutationFn: ({ id, transaction_id, ...form }: AutomationForm & { id: Uuid; transaction_id?: Uuid }) =>
      api.post<AutomationPreview>(`/assistant-automations/${id}/preview`, {
        transaction_id,
        ...form,
      }),
  })
}

export function triggerDraft(
  automation: Pick<Automation, 'filter'> | undefined,
  universe: FilterUniverse,
): FilterDraft {
  return automation?.filter ? fromFilterItems(automation.filter.items, universe) : EMPTY_DRAFT
}

/**
 * Only a transaction trigger sends conditions; for the others the stored
 * filter is left alone for a switch back.
 */
export function triggerConditions(
  trigger: AutomationTrigger,
  draft: FilterDraft,
  universe: FilterUniverse,
): Pick<AutomationForm, 'conditions'> {
  return trigger === 'transaction_arrived' ? { conditions: toFilterItems(draft, universe) } : {}
}

export function describeTrigger(
  automation: Pick<Automation, 'trigger' | 'trigger_config'> & { filter?: FilterRead | null },
): string {
  switch (automation.trigger) {
    case 'transaction_arrived': {
      const parts = ['when a transaction arrives']
      const conditions = automation.filter?.items.length ?? 0
      if (conditions > 0) {
        parts.push(`that meets ${plural(conditions, 'condition')}`)
      }
      if (automation.trigger_config.skip_transfers) parts.push('(not a transfer)')
      return parts.join(' ')
    }
    case 'daily':
      return `daily at ${automation.trigger_config.at ?? '06:00'}`
    default:
      return 'only when run by hand'
  }
}

export const RUN_TONE: Record<RunStatus, 'neutral' | 'accent' | 'income' | 'expense'> = {
  queued: 'neutral',
  running: 'accent',
  succeeded: 'income',
  failed: 'expense',
  skipped: 'neutral',
}

export function describeDecision(run: Pick<AutomationRun, 'decided_by' | 'confidence'>): string {
  const score = run.confidence === null ? '' : ` at ${run.confidence.toFixed(2)}`
  switch (run.decided_by) {
    case 'agentifi':
      return `decided from the history${score}, no model call`
    case 'model':
      return `decided by the model${score}`
    default:
      return ''
  }
}

export function describeFiredBy(firedBy: AutomationRun['fired_by']): string {
  switch (firedBy) {
    case 'transaction':
      return 'Fired by a transaction arriving'
    case 'schedule':
      return 'Fired by the schedule'
    case 'match':
      return 'Fired by an order or receipt matching the row'
    default:
      return 'Run by hand'
  }
}

const TRIGGERS: readonly AutomationTrigger[] = ['transaction_arrived', 'daily', 'manual']
const MODES: readonly AutomationMode[] = ['observe', 'propose', 'apply']
export const AUTOMATION_MODES = MODES

export function asTrigger(value: string): AutomationTrigger | null {
  return TRIGGERS.find((one) => one === value) ?? null
}

export function asMode(value: string): AutomationMode | null {
  return MODES.find((one) => one === value) ?? null
}

export function describeMode(mode: AutomationMode): string {
  switch (mode) {
    case 'observe':
      return 'observes only'
    case 'apply':
      return 'applies changes immediately'
    default:
      return 'proposes changes for review'
  }
}
