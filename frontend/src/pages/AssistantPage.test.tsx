/**
 * The assistant screen, rendered against a seeded cache. What matters is that
 * a proposed change arrives as a card, in its place in the conversation, with
 * the buttons that decide it.
 */

import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'

import {
  ASSISTANT_KEY,
  CONVERSATIONS_KEY,
  conversationKey,
  type AssistantAction,
  type AssistantStatus,
  type Conversation,
} from '@/lib/clients/assistant'
import { PENDING_KEY } from '@/lib/clients/automations'
import { CURRENT_SPACE_KEY } from '@/lib/clients/spaces'
import type { Uuid } from '@/lib/transactions/types'
import { renderScreen, type RenderScreenOptions } from '@/test/renderScreen'
import { AssistantPage } from './AssistantPage'

const CONVERSATION = '00000000-0000-0000-0000-0000000000c1' as Uuid

function status(overrides: Partial<AssistantStatus> = {}): AssistantStatus {
  return {
    configured: true,
    is_enabled: true,
    base_url: 'http://10.0.0.5:11434/v1',
    model: 'local-model',
    name: '',
    has_key: true,
    allow_writes: false,
    apply_without_asking: false,
    tool_call_style: 'native',
    tools: [
      { name: 'list_accounts', description: 'Every account.', writes: false },
      { name: 'update_transaction', description: 'Propose a change.', writes: true },
    ],
    ...overrides,
  }
}

function action(overrides: Partial<AssistantAction> = {}): AssistantAction {
  return {
    id: '00000000-0000-0000-0000-0000000000a1' as Uuid,
    tool: 'update_transaction',
    summary: 'Recategorize the coffee as Dining',
    method: 'PATCH',
    path: '/transactions/00000000-0000-0000-0000-0000000000t1',
    body: { category_id: '00000000-0000-0000-0000-0000000000c9' },
    status: 'pending',
    result: '',
    status_code: 0,
    created_at: '2026-08-01T10:00:01Z',
    decided_at: null,
    ...overrides,
  }
}

function conversation(actions: AssistantAction[]): Conversation {
  return {
    id: CONVERSATION,
    title: 'Tidy up August',
    created_at: '2026-08-01T09:59:00Z',
    updated_at: '2026-08-01T10:00:02Z',
    messages: [
      {
        id: '00000000-0000-0000-0000-0000000000m1' as Uuid,
        role: 'user',
        content: 'What is uncategorized?',
        tool_name: '',
        tool_arguments: null,
        created_at: '2026-08-01T10:00:00Z',
      },
      {
        id: '00000000-0000-0000-0000-0000000000t1' as Uuid,
        role: 'tool',
        content: '{}',
        tool_name: 'list_accounts',
        tool_arguments: null,
        created_at: '2026-08-01T10:00:00.5Z',
      },
      {
        id: '00000000-0000-0000-0000-0000000000t2' as Uuid,
        role: 'tool',
        content: '{}',
        tool_name: 'update_transaction',
        tool_arguments: null,
        created_at: '2026-08-01T10:00:01.5Z',
      },
      {
        id: '00000000-0000-0000-0000-0000000000m2' as Uuid,
        role: 'assistant',
        content: 'One change is waiting for you.',
        tool_name: '',
        tool_arguments: null,
        created_at: '2026-08-01T10:00:02Z',
      },
    ],
    actions,
  }
}

// Which thread is open is in the URL, so the address the page is rendered at
// is what decides whether these tests see a conversation or a new chat.
function render(node: ReactNode, seed: RenderScreenOptions['seed'], at = '/assistant'): string {
  return renderScreen(node, { seed, route: at })
}

function seedConversation(
  actions: AssistantAction[],
  overrides?: Partial<AssistantStatus>,
  owner = true,
): RenderScreenOptions['seed'] {
  return [
    [ASSISTANT_KEY, status(overrides)],
    [CURRENT_SPACE_KEY, { is_owner: owner }],
    [CONVERSATIONS_KEY, [conversation(actions)]],
    [conversationKey(CONVERSATION), conversation(actions)],
  ]
}

function withConversation(actions: AssistantAction[], overrides?: Partial<AssistantStatus>) {
  return render(
    <AssistantPage />,
    seedConversation(actions, overrides),
    `/assistant?conversation=${CONVERSATION}`,
  )
}

describe('the assistant screen', () => {
  it('renders the setup form with both halves of the catalogue before anything is configured', () => {
    // Somebody deciding whether to point a model at their finances is entitled
    // to see what it could do — including the changes — before switching it on.
    const markup = render(<AssistantPage />, [[ASSISTANT_KEY, status({ configured: false })]])
    expect(markup).toContain('Set up the assistant')
    expect(markup).toContain('What it can read')
    expect(markup).toContain('What it can propose')
  })

  it('opens on a new conversation rather than on the one asked last', () => {
    // Arriving here is somebody wanting to ask something, so it opens on a new
    // chat rather than the last thread.
    const markup = render(<AssistantPage />, seedConversation([]))
    expect(markup).toContain('New conversation')
    expect(markup).not.toContain('What is uncategorized?')
    expect(markup).not.toContain('aria-current')
    // And the earlier ones are still one click away.
    expect(markup).toContain('Tidy up August')
  })

  it('takes a question in a conversation that does not exist yet', () => {
    // The row is written when the question is sent, not when the tab is
    // opened, so the box has to be typeable with nothing selected.
    const markup = render(<AssistantPage />, seedConversation([]))
    const placeholder = markup.indexOf('placeholder="What did we spend')
    const box = markup.slice(
      markup.lastIndexOf('<input', placeholder),
      markup.indexOf('>', placeholder) + 1,
    )
    expect(box).not.toContain('disabled')
  })

  it('opens the thread named in the address, so a reload comes back to it', () => {
    expect(withConversation([])).toContain('What is uncategorized?')
  })

  it('shows a proposed change as a card with the request it will issue', () => {
    const markup = withConversation([action()], { allow_writes: true })
    expect(markup).toContain('Recategorize the coffee as Dining')
    expect(markup).toContain('PATCH')
    expect(markup).toContain('/transactions/00000000-0000-0000-0000-0000000000t1')
    expect(markup).toContain('Accept')
    expect(markup).toContain('Decline')
  })

  it('says a change is waiting rather than that one was made', () => {
    const markup = withConversation([action()], { allow_writes: true })
    expect(markup).toContain('1 proposed change waiting')
    expect(markup).toContain('Nothing changed yet')
  })

  it('puts the card between the turns it happened between', () => {
    // Not all the cards at the bottom: in a long thread nothing would say
    // which answer proposed which change.
    const markup = withConversation([action()], { allow_writes: true })
    const asked = markup.indexOf('What is uncategorized?')
    const card = markup.indexOf('Recategorize the coffee as Dining')
    const answered = markup.indexOf('One change is waiting for you.')
    expect(asked).toBeLessThan(card)
    expect(card).toBeLessThan(answered)
  })

  it('keeps a decided change on screen, marked, rather than removing it', () => {
    // "What did I agree to last week" is a question this page should answer.
    const markup = withConversation(
      [action({ status: 'applied', result: '{"id":"…"}', status_code: 200 })],
      { allow_writes: true },
    )
    expect(markup).toContain('Recategorize the coffee as Dining')
    expect(markup).toContain('applied')
    expect(markup).not.toContain('Apply</button>')
  })

  it('says what it can do in the subtitle, and it differs with the setting', () => {
    expect(withConversation([])).toContain('read-only')
    expect(withConversation([], { allow_writes: true })).toContain('proposes changes for you to accept')
    expect(
      withConversation([], { allow_writes: true, apply_without_asking: true }),
    ).toContain('applies changes without asking')
  })

  it('offers "apply without asking" only once changes are on', () => {
    // A control that does nothing until another control is set is a control
    // people turn on and then wonder about.
    expect(withConversation([])).not.toContain('Apply without asking')
    expect(withConversation([], { allow_writes: true })).toContain('Apply without asking')
  })

  it('does not describe a change tool as something it read', () => {
    // A tool that creates a tag must never read as "read create tag". The card
    // between the two calls keeps them on separate lines.
    const markup = withConversation([action()], { allow_writes: true })
    expect(markup).toContain('read list accounts')
    expect(markup).toContain('asked to update transaction')
    expect(markup).not.toContain('read update transaction')
  })

  it('folds a run of calls into one line rather than printing the working', () => {
    // Six of these would stand above every answer. With no card between them
    // the two calls are consecutive, which is what folds.
    const markup = withConversation([], { allow_writes: true })
    expect(markup).toContain('2 steps')
    expect(markup).not.toContain('read list accounts')
  })

  it('says what each mode does in visible text, not only in a tooltip', () => {
    // Tooltips here open neither on touch nor for a screen reader.
    expect(withConversation([], { allow_writes: true })).toContain(
      'Changes arrive as cards to accept or decline',
    )
    expect(
      withConversation([], { allow_writes: true, apply_without_asking: true }),
    ).toContain('Changes apply immediately')
  })

  it('shows a change it made as made, with no buttons to press', () => {
    // The card is the record rather than a decision: with the setting on there
    // is nothing left to decide, and offering Apply would imply otherwise.
    const markup = withConversation(
      [action({ status: 'applied', result: '{"id":"…"}', status_code: 200 })],
      { allow_writes: true, apply_without_asking: true },
    )
    expect(markup).toContain('Recategorize the coffee as Dining')
    expect(markup).toContain('applied')
    expect(markup).not.toContain('Discard')
  })

  it('does not claim anything is waiting when nothing is', () => {
    // With automatic application on there is never a pending card, so the
    // footnote says where to check instead of what to approve.
    const markup = withConversation([action({ status: 'applied', status_code: 200 })], {
      allow_writes: true,
      apply_without_asking: true,
    })
    // The footnote's own wording, not the substring: the seeded answer also
    // says "waiting for you".
    expect(markup).not.toContain('proposed change waiting')
    expect(markup).toContain('Changes apply immediately')
  })

  it('still counts a card left over from before the setting was turned on', () => {
    // Turning it on does not decide what is already pending, so the page has
    // to keep saying that something is.
    const markup = withConversation([action()], {
      allow_writes: true,
      apply_without_asking: true,
    })
    expect(markup).toContain('1 proposed change waiting')
    expect(markup).toContain('Apply')
  })

  it('names the model answering and changes it in place', () => {
    // The model is named, and changing it opens the connection form over the
    // chat. Its address is not shown until that form opens.
    const markup = withConversation([])
    expect(markup).toContain('local-model')
    expect(markup).toContain('>Change the model</button>')
    expect(markup).not.toContain('href="/settings/assistant"')
    expect(markup).not.toContain('http://10.0.0.5:11434/v1')
    expect(markup).not.toContain('Disconnect')
  })

  it('offers the model change only to whoever owns the space', () => {
    // The server refuses anyone else's change to the connection.
    const markup = render(
      <AssistantPage />,
      seedConversation([], undefined, false),
      `/assistant?conversation=${CONVERSATION}`,
    )
    expect(markup).toContain('local-model')
    expect(markup).not.toContain('>Change the model</button>')
  })

  it('has no review queue of its own: a suggestion is decided on its own row', () => {
    // A separate queue and the register would be two lists of the same
    // transactions, on two screens, and emptying either one would leave the
    // other waiting.
    const markup = withConversation([action()], { allow_writes: true })
    expect(markup).not.toContain('For your review')
    expect(markup).not.toContain('Nothing waiting')
    expect(markup).toContain('Chat')
    expect(markup).toContain('Automations')
  })

  it('sends what the automations are waiting on to the register', () => {
    const markup = render(<AssistantPage />, [
      [ASSISTANT_KEY, status({ allow_writes: true })],
      [CONVERSATIONS_KEY, [conversation([])]],
      [conversationKey(CONVERSATION), conversation([])],
      [PENDING_KEY, [{ id: 'p1' }, { id: 'p2' }]],
    ])
    expect(markup).toContain('Automations proposed 2 changes')
    expect(markup).toContain('href="/transactions?displayNode=all&amp;isReviewed=0"')
    // Its own line: the footnote beside it is about this conversation's own
    // changes, and sharing one sentence would conflate two subjects.
    expect(markup).not.toContain('2 more from automations')
  })

  it('replaces the chat with the switch when the assistant is switched off', () => {
    // Every question would have failed at the server, and a chat box that
    // errors says nothing about why.
    const markup = withConversation([], { is_enabled: false })
    expect(markup).toContain('The assistant is switched off')
    expect(markup).toContain('Assistant enabled')
    // No question box to type into while nothing would answer it.
    expect(markup).not.toContain('Ask about your money')
  })

  it('shows the email a thread is about above it, without its text', () => {
    // A thread started from the Email page carries the mail's sender, subject
    // and date. Its text is fetched by the server per question, never held here.
    const markup = render(
      <AssistantPage />,
      [
        [ASSISTANT_KEY, status()],
        [CONVERSATIONS_KEY, []],
        [
          conversationKey(CONVERSATION),
          {
            ...conversation([]),
            mail: {
              id: 'log-receipt',
              connection_id: 'mbx-bills',
              sender: 'receipts@lunch.example',
              subject: 'Your Lunch Receipt',
              received_at: '2026-09-20T08:00:00Z',
            },
          },
        ],
      ],
      `/assistant?conversation=${CONVERSATION}`,
    )
    expect(markup).toContain('About the email:')
    expect(markup).toContain('Your Lunch Receipt')
    expect(markup).toContain('from receipts@lunch.example')
  })

  it('starts a thread about an email handed over in the address', () => {
    // The effect that posts it does not run in a static render; the page opens
    // on the new-chat pane rather than an unrelated thread meanwhile.
    const markup = render(<AssistantPage />, seedConversation([]), '/assistant?mail=log-receipt')
    expect(markup).not.toContain('What is uncategorized?')
  })
})
