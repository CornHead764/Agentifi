import type { AssistantAction, AssistantMessage, Conversation } from '@/lib/clients/assistant'
import type { Uuid } from '@/lib/transactions/types'

/**
 * What to make active after `deletedId` is gone: unchanged unless it was
 * active, then the next in the list, or `null` so the caller starts fresh.
 */
export function nextActiveConversation(
  conversations: readonly Pick<Conversation, 'id'>[],
  activeId: Uuid | null,
  deletedId: Uuid,
): Uuid | null {
  if (activeId !== deletedId) return activeId
  return conversations.find((one) => one.id !== deletedId)?.id ?? null
}

export function conversationLabel(conversation: Pick<Conversation, 'title'>): string {
  return conversation.title.trim() === '' ? 'New conversation' : conversation.title
}

export type TimelineEntry =
  | { kind: 'message'; at: string; message: AssistantMessage }
  | { kind: 'action'; at: string; action: AssistantAction }

export type GroupedEntry =
  | TimelineEntry
  | { kind: 'tools'; at: string; messages: AssistantMessage[] }
  | { kind: 'group'; at: string; group: Uuid; actions: AssistantAction[] }

/**
 * Turns and proposed changes, interleaved in time order. Stamps are parsed,
 * not compared as strings: Go drops trailing zeros from the fraction, so
 * `10:00:00.5Z` sorts before `10:00:00Z` as text. The app's own action lines
 * are left out; they are written for the model.
 */
export function conversationTimeline(
  conversation: Pick<Conversation, 'messages' | 'actions'> | undefined,
): TimelineEntry[] {
  const entries: TimelineEntry[] = [
    ...(conversation?.messages ?? [])
      .filter((message) => message.role !== 'action')
      .map((message): TimelineEntry => ({ kind: 'message', at: message.created_at, message })),
    ...(conversation?.actions ?? []).map(
      (action): TimelineEntry => ({ kind: 'action', at: action.created_at, action }),
    ),
  ]
  return entries.sort((left, right) => Date.parse(left.at) - Date.parse(right.at))
}

/**
 * Fold runs of consecutive tool calls into one entry, and a bulk proposal's
 * cards into one group where the first was. A lone tool call between answers
 * stays where it happened.
 */
export function groupToolRuns(entries: readonly TimelineEntry[]): GroupedEntry[] {
  const out: GroupedEntry[] = []
  const groups = new Map<Uuid, Extract<GroupedEntry, { kind: 'group' }>>()
  for (const entry of entries) {
    if (entry.kind === 'action' && entry.action.group_id) {
      const existing = groups.get(entry.action.group_id)
      if (existing) {
        existing.actions.push(entry.action)
        continue
      }
      const group = {
        kind: 'group' as const,
        at: entry.at,
        group: entry.action.group_id,
        actions: [entry.action],
      }
      groups.set(entry.action.group_id, group)
      out.push(group)
      continue
    }
    const isTool = entry.kind === 'message' && entry.message.role === 'tool'
    if (!isTool) {
      out.push(entry)
      continue
    }
    const last = out[out.length - 1]
    if (last !== undefined && last.kind === 'tools') {
      last.messages.push(entry.message)
      continue
    }
    out.push({ kind: 'tools', at: entry.at, messages: [entry.message] })
  }
  return out
}
