/** The assistant. The API key is write-only: only `has_key` ever comes back. */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api'
import { useInvalidatingMutation } from '@/lib/queryClient'
import type { Uuid } from '@/lib/transactions/types'

export interface AssistantTool {
  name: string
  description: string
  /** Proposes a change; never makes one on its own. */
  writes: boolean
}

/** `method`, `path` and `body` are the request exactly as it will be issued. */
export interface AssistantAction {
  id: Uuid
  tool: string
  summary: string
  method: string
  path: string
  body: Record<string, unknown> | null
  /** Present only when a category was overridden; `body` is what ran. */
  proposed_body?: Record<string, unknown> | null
  /**
   * `failed` may be applied again, since a refused request changed nothing.
   * `simulated` is a dry run's record: terminal, no Apply.
   */
  status: ActionStatus
  /** Empty while pending. */
  result: string
  status_code: number
  created_at: string
  decided_at: string | null
  about?: ActionAbout | null
  /** Optional: not every card carries a preview. */
  preview?: ActionPreview | null
  /** Joins the cards one bulk proposal made. */
  group_id?: Uuid | null
  resource_id?: Uuid | null
  decline_reason?: string
}

export type ActionStatus = 'pending' | 'applying' | 'applied' | 'failed' | 'discarded' | 'simulated'

export interface PreviewField {
  label: string
  value: string
  /** `money` is a wire amount string; `name` is an id the server has named. */
  kind: 'money' | 'name' | 'flag' | 'text'
}

export interface ActionPreview {
  /** The first segment of the path. */
  resource: string
  verb: 'create' | 'update' | 'delete'
  fields: PreviewField[]
  /** Keyed by id or `@action:` placeholder. */
  names: Record<string, string>
  group_summary?: string
}

/** The row a change names. Amounts are wire strings, parsed where shown. */
export interface ActionAbout {
  transaction_id: Uuid
  account_id: Uuid
  date: string
  statement_name: string
  payee: string
  amount: string
  /** Absent for a split, where each part carries its own item's name. */
  items?: { title: string; amount?: string }[]
}

export interface AssistantStatus {
  configured: boolean
  is_enabled: boolean
  base_url: string
  model: string
  name: string
  has_key: boolean
  allow_writes: boolean
  /** Only meaningful with `allow_writes`; the server clears it when that goes off. */
  apply_without_asking: boolean
  /** `prompted` is for a server with a model but no tool-call parser. */
  tool_call_style: 'native' | 'prompted'
  /** Every tool, whether or not writes are allowed. */
  tools: AssistantTool[]
}

/** Which tools change something, so a call to one is never described as a read. */
export function writeToolNames(tools: readonly AssistantTool[] | undefined): Set<string> {
  return new Set((tools ?? []).filter((tool) => tool.writes).map((tool) => tool.name))
}

export interface AssistantMessage {
  id: Uuid
  /** `action` records a card's outcome for the model; the thread does not draw it. */
  role: 'user' | 'assistant' | 'tool' | 'action'
  content: string
  tool_name: string
  tool_arguments: Record<string, unknown> | null
  created_at: string
}

export interface Conversation {
  id: Uuid
  title: string
  created_at: string
  updated_at: string
  messages: AssistantMessage[]
  actions: AssistantAction[]
  /** The mail's text is never kept; the server fetches it for each question. */
  mail?: ConversationMail | null
}

export interface ConversationMail {
  id: Uuid
  connection_id: Uuid
  sender: string
  subject: string
  received_at: string
}

export interface Answer {
  answer: string
  tool_calls: string[]
  conversation: Conversation
}

export const ASSISTANT_KEY = ['assistant'] as const
export const CONVERSATIONS_KEY = ['assistant', 'conversations'] as const

export function conversationKey(id: Uuid | null) {
  return ['assistant', 'conversations', id ?? 'none'] as const
}

export function useAssistantStatus(enabled = true) {
  return useQuery({
    queryKey: ASSISTANT_KEY,
    enabled,
    queryFn: ({ signal }) => api.get<AssistantStatus>('/assistant', undefined, signal),
  })
}

export function useConversations() {
  return useQuery({
    queryKey: CONVERSATIONS_KEY,
    queryFn: ({ signal }) => api.get<Conversation[]>('/assistant/conversations', undefined, signal),
  })
}

export function useConversation(id: Uuid | null) {
  return useQuery({
    queryKey: conversationKey(id),
    queryFn: ({ signal }) =>
      api.get<Conversation>(`/assistant/conversations/${id}`, undefined, signal),
    enabled: id !== null,
  })
}

export interface ConnectionForm {
  base_url: string
  model: string
  name?: string
  /** Every optional field: omitted leaves the stored value alone. */
  api_key?: string
  is_enabled?: boolean
  allow_writes?: boolean
  apply_without_asking?: boolean
  tool_call_style?: 'native' | 'prompted'
}

export interface ConnectionTest {
  reachable: boolean
  native_tool_calls: boolean
  prompted_tool_calls: boolean
  detail: string
  recommended: 'native' | 'prompted' | ''
}

export function useTestConnection() {
  return useMutation({
    mutationFn: () => api.post<ConnectionTest>('/assistant/connection/test'),
  })
}

export function useSaveConnection() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (form: ConnectionForm) =>
      api.put<AssistantStatus>('/assistant/connection', form),
    onSuccess: (status) => client.setQueryData(ASSISTANT_KEY, status),
  })
}

export function useForgetConnection() {
  return useInvalidatingMutation(
    () => api.delete<void>('/assistant/connection'),
    [ASSISTANT_KEY],
    { failure: 'The connection was not removed' },
  )
}

export function useDeleteConversation() {
  const client = useQueryClient()
  return useInvalidatingMutation(
    (id: Uuid) => api.delete<void>(`/assistant/conversations/${id}`),
    [CONVERSATIONS_KEY],
    {
      failure: 'That conversation was not deleted',
      onSuccess: (_data, id) => client.removeQueries({ queryKey: conversationKey(id) }),
    },
  )
}

/**
 * `id` is null for an unsent chat, which exists only in the browser; the
 * conversation is created just before the first question. `onThread` hands
 * over the new id before the answer, so a failed answer still points the page
 * at its thread.
 */
export function useAsk(onThread?: (id: Uuid) => void) {
  const client = useQueryClient()
  return useInvalidatingMutation(
    async ({ id, question }: { id: Uuid | null; question: string }) => {
      let target = id
      if (target === null) {
        const made = await api.post<Conversation>('/assistant/conversations')
        client.setQueryData(conversationKey(made.id), made)
        target = made.id
        onThread?.(made.id)
      }
      return api.post<Answer>(`/assistant/conversations/${target}/ask`, { question })
    },
    [CONVERSATIONS_KEY],
    {
      failure: 'That question was not answered',
      onSuccess: (answer) => {
        client.setQueryData(conversationKey(answer.conversation.id), answer.conversation)
      },
    },
  )
}

export function startMailConversation(mailId: Uuid): Promise<Conversation> {
  return api.post<Conversation>('/assistant/conversations', { mail_id: mailId })
}

export function useStartMailConversation() {
  const client = useQueryClient()
  return useInvalidatingMutation(startMailConversation, [CONVERSATIONS_KEY], {
    onSuccess: (made) => client.setQueryData(conversationKey(made.id), made),
  })
}

/** Absent keeps the model's choice; an empty string means uncategorized. */
export interface ApplyOverrides {
  category_id?: string
  split_categories?: { index: number; category_id: string }[]
}

/** Exported so the register decides proposals through the same paths. */
export function applyAssistantAction(id: Uuid, overrides?: ApplyOverrides) {
  return api.post<AssistantAction>(`/assistant-actions/${id}/apply`, overrides)
}

/** The reason goes to the model with the person's next message. */
export function discardAssistantAction(id: Uuid, reason?: string) {
  return api.post<AssistantAction>(
    `/assistant-actions/${id}/discard`,
    reason && reason.trim() !== '' ? { reason: reason.trim() } : undefined,
  )
}

/**
 * Applied in proposal order, so a rule runs after a category proposed beside
 * it. A card already decided comes back as it stands.
 */
function applyManyAssistantActions(ids: readonly Uuid[]) {
  return api.post<{ actions: AssistantAction[] }>('/assistant-actions/apply-many', {
    action_ids: ids,
  })
}

function markRunning(
  client: ReturnType<typeof useQueryClient>,
  conversationId: Uuid,
  ids: readonly Uuid[],
) {
  const key = conversationKey(conversationId)
  const running = new Set(ids)
  client.setQueryData<Conversation>(key, (current) =>
    current === undefined
      ? current
      : {
          ...current,
          actions: current.actions.map((action) =>
            running.has(action.id) && (action.status === 'pending' || action.status === 'failed')
              ? { ...action, status: 'applying' }
              : action,
          ),
        },
  )
}

export function useApplyAction() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, overrides }: { id: Uuid; conversationId: Uuid; overrides?: ApplyOverrides }) =>
      applyAssistantAction(id, overrides),
    onMutate: ({ id, conversationId }) => markRunning(client, conversationId, [id]),
    // Everything: the applied request could have changed any resource.
    onSettled: () => void client.invalidateQueries(),
  })
}

export function useApplyManyActions() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ ids }: { ids: readonly Uuid[]; conversationId: Uuid }) =>
      applyManyAssistantActions(ids),
    onMutate: ({ ids, conversationId }) => markRunning(client, conversationId, ids),
    onSettled: () => void client.invalidateQueries(),
  })
}

export function useDiscardAction() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({ id, reason }: { id: Uuid; conversationId: Uuid; reason?: string }) =>
      discardAssistantAction(id, reason),
    onSettled: (_data, _error, { conversationId }) =>
      void client.invalidateQueries({ queryKey: conversationKey(conversationId) }),
  })
}

export function useDiscardManyActions() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ ids, reason }: { ids: readonly Uuid[]; conversationId: Uuid; reason?: string }) => {
      for (const id of ids) await discardAssistantAction(id, reason)
    },
    onSettled: (_data, _error, { conversationId }) =>
      void client.invalidateQueries({ queryKey: conversationKey(conversationId) }),
  })
}
