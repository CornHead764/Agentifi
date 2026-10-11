/**
 * What a category run decided, read off the proposal it left: the shape the
 * hover card over a suggestion draws, so the run and the card say the same.
 */

import type { AssistantAction } from '@/lib/clients/assistant'
import type { AutomationRun } from '@/lib/clients/automations'
import { parseMoney, type Money } from '@/lib/money'
import { isRecord } from '@/lib/typeGuards'
import { suggestionReason } from '@/lib/transactions/suggestions'
import type { SuggestionBasis, Uuid } from '@/lib/transactions/types'

export interface DecisionLine {
  categoryId: Uuid | null
  confidence: number | null
  items: string[]
  amount: Money | null
}

export interface RunDecision {
  split: boolean
  lines: DecisionLine[]
  /** Names for every id the proposal mentions. */
  names: Record<string, string>
  basis: SuggestionBasis | ''
  sentence: string
  status: AssistantAction['status']
}

const CATEGORY_TOOLS = ['update_transaction', 'split_transaction']

function text(value: unknown): string | null {
  return typeof value === 'string' && value !== '' ? value : null
}

function money(value: unknown): Money | null {
  const wire = text(value)
  return wire === null ? null : parseMoney(wire)
}

/** The run's last category proposal, or null when it proposed none. */
export function runDecision(
  run: Pick<AutomationRun, 'confidence' | 'conversation'>,
): RunDecision | null {
  const action = (run.conversation?.actions ?? [])
    .filter((one) => CATEGORY_TOOLS.includes(one.tool))
    .at(-1)
  if (!action) return null
  const body = action.body ?? {}
  const preview = action.preview
  const basis: RunDecision['basis'] = preview?.basis ?? ''
  const shared = {
    names: preview?.names ?? {},
    basis,
    sentence: suggestionReason(action.summary),
    status: action.status,
  }

  if (action.tool === 'split_transaction') {
    const parts = Array.isArray(body.splits) ? body.splits.filter(isRecord) : []
    const confidences = preview?.split_confidences ?? []
    return {
      ...shared,
      split: true,
      lines: parts.map((part, index) => ({
        categoryId: text(part.category_id),
        confidence: confidences[index] ?? null,
        items: text(part.memo)?.split(', ') ?? [],
        amount: money(part.amount),
      })),
    }
  }

  const categoryId = text(body.category_id)
  if (categoryId === null) return null
  return {
    ...shared,
    split: false,
    lines: [
      {
        categoryId,
        confidence: preview?.confidence ?? run.confidence,
        items: (action.about?.items ?? []).map((item) => item.title),
        amount: money(action.about?.amount),
      },
    ],
  }
}
