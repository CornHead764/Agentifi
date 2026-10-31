/**
 * Asking the assistant to suggest categories for named rows. Each suggestion
 * waits on its row to be accepted. When nothing could run, the toast that says
 * so carries the one step that fixes it, and taking that step asks again.
 */

import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'

import { ConfirmDialog, failureToast, useToast, type ToastOptions } from '@/components/ui'
import { api, ApiError } from '@/lib/api'
import {
  ASSISTANT_KEY,
  useAssistantStatus,
  useSaveConnection,
  type AssistantStatus,
} from '@/lib/clients/assistant'
import {
  AUTOMATIONS_KEY,
  useFireAutomations,
  useUpdateAutomation,
  type Automation,
  type FireTarget,
} from '@/lib/clients/automations'
import { asksFirst, bulkSuggestionPrompt } from '@/lib/clients/suggestionBatches'
import type { RegisterQuery } from '@/lib/transactions/api'
import { TRANSACTIONS_KEY } from '@/lib/transactions/cache'
import type { Uuid } from '@/lib/transactions/types'

import { AssistantSetupDialog } from './connection'

/** The built-in that suggests categories, by the key the server gives it. */
export const SUGGEST_TEMPLATE_KEY = 'check_category'

/** Why nothing ran, as the server's refusal codes it. */
export type SuggestBlock = 'assistant_unavailable' | 'changes_off' | 'automation_off'

export function suggestBlock(error: unknown): SuggestBlock | null {
  if (!(error instanceof ApiError) || error.status !== 409) return null
  const code = error.code
  return code === 'assistant_unavailable' || code === 'changes_off' || code === 'automation_off'
    ? code
    : null
}

/** The toast for a refusal, its action left to the caller. */
export function blockedToast(block: SuggestBlock): Omit<ToastOptions, 'action'> & { step: string } {
  switch (block) {
    case 'assistant_unavailable':
      return {
        title: 'Set up the assistant first',
        description: 'Suggesting categories asks the assistant, which is not set up or is switched off.',
        step: 'Set up the assistant',
      }
    case 'changes_off':
      return {
        title: 'Suggestions are off',
        description:
          'The assistant may not propose changes. Turning that on lets it suggest categories for you to accept; nothing changes until you do.',
        step: 'Turn on suggestions',
      }
    case 'automation_off':
      return {
        title: 'Suggest categories is switched off',
        description: 'It is switched off among the assistant’s automations.',
        step: 'Turn it on',
      }
  }
}

/** Named rows, or every row the register's query matches and how many that is. */
export type SuggestTarget = { ids: Uuid[] } | { query: RegisterQuery; count: number }

export function suggestCount(target: SuggestTarget): number {
  return 'ids' in target ? target.ids.length : target.count
}

function fireTarget(target: SuggestTarget): FireTarget {
  return 'ids' in target ? { ids: target.ids } : { query: target.query }
}

export interface SuggestCategories {
  /**
   * Ask for suggestions, first asking whether to when there are more than the
   * bulk size. `onQueued` hears the named rows once any run is queued, and no
   * rows for a query, whose loaded rows are refetched to show their spinners.
   */
  suggest: (target: SuggestTarget, onQueued: (ids: readonly Uuid[]) => void) => void
  busy: boolean
  /** The dialogs the confirmation and the set-up step open. Render them once, anywhere. */
  dialog: ReactNode
}

export function useSuggestCategories(): SuggestCategories {
  const fire = useFireAutomations()
  const save = useSaveConnection()
  const update = useUpdateAutomation()
  const status = useAssistantStatus()
  const client = useQueryClient()
  const toast = useToast()
  const navigate = useNavigate()
  const [afterSetup, setAfterSetup] = useState<(() => void) | null>(null)
  const [asking, setAsking] = useState<{ count: number; go: () => void } | null>(null)

  const fireRows = fire.mutate
  const saveConnection = save.mutate
  const updateAutomation = update.mutate
  const stored = status.data

  const suggest = useCallback(
    (target: SuggestTarget, onQueued: (ids: readonly Uuid[]) => void) => {
      const setUp = (again: () => void) => setAfterSetup(() => again)

      const fix = (block: SuggestBlock, again: () => void) => {
        if (block === 'assistant_unavailable' || !stored?.configured) {
          setUp(again)
          return
        }
        if (block === 'changes_off') {
          saveConnection(
            { base_url: stored.base_url, model: stored.model, name: stored.name, allow_writes: true },
            { onSuccess: again },
          )
          return
        }
        void client
          .fetchQuery({
            queryKey: AUTOMATIONS_KEY,
            queryFn: ({ signal }) =>
              api.get<Automation[]>('/assistant-automations', undefined, signal),
          })
          .then((automations) => {
            const builtIn = automations.find((one) => one.template_key === SUGGEST_TEMPLATE_KEY)
            if (builtIn === undefined) {
              navigate('/assistant')
              return
            }
            updateAutomation({ id: builtIn.id, is_enabled: true }, { onSuccess: again })
          })
      }

      const attempt = () =>
        fireRows(
          { target: fireTarget(target), force: true },
          {
            onSuccess: ({ queued }) => {
              if (queued === 0) {
                toast.show({
                  title: 'Nothing to suggest',
                  description: 'These rows are already waiting for a suggestion.',
                })
                return
              }
              // No toast: the rows carry their own spinners and the strip counts
              // the request off.
              if ('ids' in target) {
                onQueued(target.ids)
                return
              }
              void client.invalidateQueries({ queryKey: TRANSACTIONS_KEY })
              onQueued([])
            },
            onError: (error) => {
              const block = suggestBlock(error)
              if (block === null) {
                toast.show(failureToast(error, 'Categories were not suggested'))
                return
              }
              const { step, ...shown } = blockedToast(block)
              toast.show({ ...shown, action: { label: step, onSelect: () => fix(block, attempt) } })
            },
          },
        )
      const count = suggestCount(target)
      if (asksFirst(count)) {
        setAsking({ count, go: attempt })
        return
      }
      attempt()
    },
    [client, fireRows, navigate, saveConnection, stored, toast, updateAutomation],
  )

  const prompt = bulkSuggestionPrompt(asking?.count ?? 0)
  const dialog = (
    <>
      <ConfirmDialog
        open={asking !== null}
        title={prompt.title}
        description={prompt.description}
        confirmLabel={prompt.confirmLabel}
        onConfirm={() => {
          const go = asking?.go
          setAsking(null)
          go?.()
        }}
        onCancel={() => setAsking(null)}
      />
      <AssistantSetupDialog
        open={afterSetup !== null}
        onClose={() => {
          const again = afterSetup
          setAfterSetup(null)
          const now = client.getQueryData<AssistantStatus>(ASSISTANT_KEY)
          if (again !== null && now?.configured && now.is_enabled) again()
        }}
      />
    </>
  )

  return { suggest, busy: fire.isPending || save.isPending || update.isPending, dialog }
}
