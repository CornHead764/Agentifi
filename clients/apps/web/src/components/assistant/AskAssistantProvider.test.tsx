/**
 * The Ask dialog, rendered against a seeded thread. It names its row in the
 * app's money and dates before anything is sent, and renders the answer's
 * Markdown formatted.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import { transactionSubject } from '@/lib/assistant/subjects'
import {
  conversationKey,
  type AssistantStatus,
  type Conversation,
} from '@/lib/clients/assistant'
import { moneyFromCents } from '@/lib/money'
import type { Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { AskDialog } from './AskAssistantProvider'

const THREAD = '00000000-0000-0000-0000-0000000000c1' as Uuid

const STATUS: AssistantStatus = {
  configured: true,
  is_enabled: true,
  base_url: 'http://10.0.0.5:11434/v1',
  model: 'local-model',
  name: '',
  has_key: true,
  allow_writes: false,
  apply_without_asking: false,
  tool_call_style: 'native',
  tools: [{ name: 'read_endpoint', description: 'Read any endpoint.', writes: false }],
}

const CHARGE = transactionSubject(
  {
    id: '00000000-0000-0000-0000-00000000f001',
    account_id: '00000000-0000-0000-0000-00000000a001',
    payee: 'Barnaby Provisions',
    statement_name: 'BARNABY PROV #4471',
    amount: moneyFromCents(-8400),
    date: '2026-09-12',
    splits: [],
  },
  { account: 'Everyday Checking', category: 'Groceries' },
)

const ANSWERED: Conversation = {
  id: THREAD,
  title: 'Barnaby Provisions',
  created_at: '2026-09-12T10:00:00Z',
  updated_at: '2026-09-12T10:00:03Z',
  messages: [
    {
      id: '00000000-0000-0000-0000-0000000000m1' as Uuid,
      role: 'tool',
      content: '{"payee":"Barnaby Provisions"}',
      tool_name: 'read_endpoint',
      tool_arguments: { path: '/transactions/00000000-0000-0000-0000-00000000f001' },
      created_at: '2026-09-12T10:00:01Z',
    },
    {
      id: '00000000-0000-0000-0000-0000000000m2' as Uuid,
      role: 'assistant',
      content: 'It is filed under **Groceries**.\n\n- Six charges in three months\n- All the same shop',
      tool_name: '',
      tool_arguments: null,
      created_at: '2026-09-12T10:00:03Z',
    },
  ],
  actions: [],
}

function render(seed?: Conversation): string {
  return renderScreen(
    <AskDialog
      subject={CHARGE}
      status={STATUS}
      thread={seed ? seed.id : null}
      onThread={() => {}}
      onClose={() => {}}
    />,
    { seed: seed ? [[conversationKey(seed.id), seed]] : [] },
  )
}

describe('the Ask dialog', () => {
  const fresh = render()

  it('names the row it is about, in the money and dates the app uses', () => {
    expect(fresh).toContain('Asking about: Barnaby Provisions, -$84.00, Sep 12')
    expect(fresh).toContain('Everyday Checking')
  })

  it('names the kind of thing in its heading', () => {
    expect(fresh).toContain('Ask about this transaction')
  })

  it('offers questions to start from', () => {
    expect(fresh).toContain('Why is this categorized the way it is?')
    expect(fresh).toContain('How much do I usually spend here?')
  })

  it('asks through the same composer as the chat page, which holds Enter while an input method composes', () => {
    // One `Thread` for both screens, so the IME guard (`sendsOnEnter`) cannot
    // be on one screen and missing from the other.
    expect(fresh).toContain('aria-label="Question"')
    expect(fresh).toContain('chat__input')
  })

  it('has no way out to a thread that does not exist yet', () => {
    expect(fresh).not.toContain('Continue in Assistant')
  })
})

describe('with an answer', () => {
  it('renders the reply as Markdown, not as the characters the model typed', () => {
    // What this guards against: `**Groceries**` on screen with its asterisks.
    const answered = renderAnswered()
    expect(answered).toContain('<strong>Groceries</strong>')
    expect(answered).not.toContain('**Groceries**')
    expect(answered).toContain('<li>Six charges in three months</li>')
  })

  it('shows what it read, so the figures can be checked against their source', () => {
    expect(renderAnswered()).toContain('read endpoint')
  })

  it('offers the way on into the full conversation', () => {
    const answered = renderAnswered()
    expect(answered).toContain('Continue in Assistant')
    expect(answered).toContain(`/assistant?conversation=${THREAD}`)
  })
})

/** The dialog once a question has been asked and answered in it. */
function renderAnswered(): string {
  return render(ANSWERED)
}
