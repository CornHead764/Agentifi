/**
 * The setup guide of the space in view. Whether a step is done is the
 * server's reading of the ledger, never a click remembered here; what is
 * stored is only that the guide was hidden and which steps do not apply.
 */

import { useQuery } from '@tanstack/react-query'

import {
  GetSetupGuideResponseSchema,
  HouseholdService,
  UpdateSetupGuideRequestSchema,
  UpdateSetupGuideResponseSchema,
} from '@/gen/agentifi/v1/household_pb'
import { useInvalidatingMutation } from '@/lib/queryClient'
import { fromWire, toPatch } from '@/lib/rpc/wire'
import { rpcClient, unary } from '@/lib/rpcSession'

import { useCurrentSpace } from './spaces'

export type SetupStepId = 'import' | 'connect' | 'match' | 'assistant' | 'bills' | 'backups'

export type SetupStepState = 'done' | 'skipped' | 'open'

export interface SetupStep {
  id: SetupStepId
  state: SetupStepState
  /** An optional step does not keep the guide open. */
  optional: boolean
}

export interface SetupGuide {
  dismissed: boolean
  /** Every step done or skipped. */
  complete: boolean
  steps: SetupStep[]
  /**
   * What somebody said does not apply. A step can also be skipped because it
   * no longer can be done, such as an import into a space with accounts.
   */
  skipped: SetupStepId[]
}

export const SETUP_GUIDE_KEY = ['setup-guide'] as const

const households = rpcClient(HouseholdService)

export function useSetupGuide(enabled = true) {
  return useQuery({
    queryKey: SETUP_GUIDE_KEY,
    queryFn: ({ signal }) =>
      unary(HouseholdService.method.getSetupGuide, async () => {
        const guide = await households.getSetupGuide({}, { signal })
        return fromWire<{ guide: SetupGuide }>(GetSetupGuideResponseSchema, guide).guide
      }),
    // Steps are finished on other pages; coming back must read them afresh.
    staleTime: 0,
    enabled,
  })
}

/**
 * The guide, when it is for this person to see now: they can act on it, it
 * is not hidden, and something is left to do. Null otherwise.
 */
export function useVisibleSetupGuide(): SetupGuide | null {
  const space = useCurrentSpace()
  const canWrite = space.data?.can_write ?? false
  const guide = useSetupGuide(canWrite)
  if (!canWrite || !guide.data) return null
  return isGuideShown(guide.data) ? guide.data : null
}

export function isGuideShown(guide: SetupGuide): boolean {
  return !guide.dismissed && !guide.complete
}

/** `skipped` is the whole list; matching is never in it, as it follows connecting. */
export function useSaveSetupGuide() {
  return useInvalidatingMutation(
    (change: { dismissed?: boolean; skipped?: SetupStepId[] }) =>
      unary(HouseholdService.method.updateSetupGuide, async () => {
        const saved = await households.updateSetupGuide(toPatch(UpdateSetupGuideRequestSchema, {}, change))
        return fromWire<{ guide: SetupGuide }>(UpdateSetupGuideResponseSchema, saved).guide
      }),
    [SETUP_GUIDE_KEY],
    { failure: 'The setup guide was not saved' },
  )
}

/** The skipped list with one step added or taken out. */
export function withSkipped(guide: SetupGuide, id: SetupStepId, skip: boolean): SetupStepId[] {
  const others = guide.skipped.filter((one) => one !== id)
  return skip ? [...others, id] : others
}
