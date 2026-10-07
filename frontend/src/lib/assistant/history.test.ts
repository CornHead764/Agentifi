import { describe, expect, it } from 'vitest'

import type { AssistantAction, AssistantMessage } from '@/lib/clients/assistant'

import {
  conversationLabel,
  conversationTimeline,
  groupToolRuns,
  nextActiveConversation,
} from './history'

function id(n: number): string {
  return `00000000-0000-0000-0000-00000000000${n}`
}

describe('nextActiveConversation', () => {
  it('leaves the active conversation alone when a different one was deleted', () => {
    const conversations = [{ id: id(1) }, { id: id(2) }, { id: id(3) }]
    expect(nextActiveConversation(conversations, id(2), id(3))).toBe(id(2))
  })

  it('falls to the next most recent conversation when the active one is deleted', () => {
    // Ordered most-recently-touched first, the way the server returns it.
    const conversations = [{ id: id(1) }, { id: id(2) }, { id: id(3) }]
    expect(nextActiveConversation(conversations, id(1), id(1))).toBe(id(2))
  })

  it('returns null when the deleted conversation was the only one', () => {
    const conversations = [{ id: id(1) }]
    expect(nextActiveConversation(conversations, id(1), id(1))).toBeNull()
  })

  it('returns null rather than the deleted id when the list omits it already', () => {
    expect(nextActiveConversation([], id(1), id(1))).toBeNull()
  })

  it('leaves no conversation active alone', () => {
    const conversations = [{ id: id(1) }, { id: id(2) }]
    expect(nextActiveConversation(conversations, null, id(2))).toBeNull()
  })
})

describe('conversationLabel', () => {
  it('reads a set title', () => {
    expect(conversationLabel({ title: 'What did we spend on groceries?' })).toBe(
      'What did we spend on groceries?',
    )
  })

  it('labels a conversation with no title yet, rather than showing blank', () => {
    expect(conversationLabel({ title: '' })).toBe('New conversation')
  })

  it('treats whitespace-only title the same as no title', () => {
    expect(conversationLabel({ title: '   ' })).toBe('New conversation')
  })
})

describe('conversationTimeline', () => {
  function message(at: string, content: string): AssistantMessage {
    return {
      id: id(1) as AssistantMessage['id'],
      role: 'assistant',
      content,
      tool_name: '',
      tool_arguments: null,
      created_at: at,
    }
  }

  function action(at: string, summary: string): AssistantAction {
    return {
      id: id(2) as AssistantAction['id'],
      tool: 'update_transaction',
      summary,
      method: 'PATCH',
      path: '/transactions/x',
      body: null,
      status: 'pending',
      result: '',
      status_code: 0,
      created_at: at,
      decided_at: null,
    }
  }

  it('reads in the order things happened, not messages then cards', () => {
    const timeline = conversationTimeline({
      messages: [
        message('2026-08-01T10:00:00Z', 'first'),
        message('2026-08-01T10:00:02Z', 'second'),
      ],
      actions: [action('2026-08-01T10:00:01Z', 'a change')],
    })

    expect(timeline.map((entry) => entry.kind)).toEqual(['message', 'action', 'message'])
  })

  it('is empty for a conversation that has not loaded', () => {
    expect(conversationTimeline(undefined)).toEqual([])
  })
})

/** Go drops trailing zeros from the fraction, so '.' vs 'Z' misorders stamps as text. */
describe('conversationTimeline ordering', () => {
  function turn(id: string, at: string) {
    return { id, role: 'tool', content: '', tool_name: id, tool_arguments: null, created_at: at }
  }

  it('orders a whole second before the fraction after it', () => {
    const timeline = conversationTimeline({
      messages: [turn('b', '2026-08-01T10:00:00.5Z'), turn('a', '2026-08-01T10:00:00Z')],
      actions: [],
    } as never)
    expect(timeline.map((one) => one.kind === 'message' && one.message.id)).toEqual(['a', 'b'])
  })
})

describe('groupToolRuns', () => {
  function turn(role: 'user' | 'assistant' | 'tool', at: string) {
    return {
      kind: 'message' as const,
      at,
      message: { id: at, role, content: '', tool_name: 't', tool_arguments: null, created_at: at },
    }
  }

  it('folds a run of consecutive tool calls into one entry', () => {
    const grouped = groupToolRuns([
      turn('user', '1'),
      turn('tool', '2'),
      turn('tool', '3'),
      turn('tool', '4'),
      turn('assistant', '5'),
    ] as never)
    expect(grouped.map((one) => one.kind)).toEqual(['message', 'tools', 'message'])
    expect(grouped[1].kind === 'tools' && grouped[1].messages.length).toBe(3)
  })

  // A lone call between two answers stays where it happened.
  it('does not fold across an answer', () => {
    const grouped = groupToolRuns([
      turn('tool', '1'),
      turn('assistant', '2'),
      turn('tool', '3'),
    ] as never)
    expect(grouped.map((one) => one.kind)).toEqual(['tools', 'message', 'tools'])
  })

  it('leaves a timeline with no tool calls exactly as it was', () => {
    const entries = [turn('user', '1'), turn('assistant', '2')] as never
    expect(groupToolRuns(entries)).toEqual(entries)
  })
})

describe('what the thread draws of a card', () => {
  function card(cardId: string, group: string | null, at: string) {
    return {
      kind: 'action' as const,
      at,
      action: { id: cardId, group_id: group, status: 'pending', created_at: at } as unknown as AssistantAction,
    }
  }

  it('draws a bulk proposal as one entry, where its first card was', () => {
    const grouped = groupToolRuns([
      card('a', 'g1', '1'),
      card('b', 'g1', '2'),
      card('c', null, '3'),
      card('d', 'g1', '4'),
    ])
    expect(grouped.map((one) => one.kind)).toEqual(['group', 'action'])
    const group = grouped[0]
    expect(group.kind === 'group' && group.actions.map((one) => one.id)).toEqual(['a', 'b', 'd'])
  })

  it('leaves out the lines the app writes for the model about decided cards', () => {
    // The card already says it was accepted or declined.
    const timeline = conversationTimeline({
      messages: [
        { id: 'q', role: 'user', content: 'hi', tool_name: '', tool_arguments: null, created_at: '2026-08-01T10:00:00Z' },
        {
          id: 'o',
          role: 'action',
          content: 'The person declined …',
          tool_name: 'create_tag',
          tool_arguments: null,
          created_at: '2026-08-01T10:00:01Z',
        },
      ],
      actions: [],
    } as never)
    expect(timeline.map((one) => one.kind === 'message' && one.message.id)).toEqual(['q'])
  })
})
