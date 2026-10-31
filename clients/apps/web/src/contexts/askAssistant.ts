import { createContext, useContext } from 'react'

import type { AskSubject } from '@/lib/assistant/rowQuestion'

export interface AskAssistantValue {
  /** The provider is mounted and a model is configured and on; menus hide "Ask" otherwise. */
  ready: boolean
  ask: (subject: AskSubject) => void
}

const NOT_READY: AskAssistantValue = {
  ready: false,
  ask: () => {},
}

export const AskAssistantContext = createContext<AskAssistantValue>(NOT_READY)

/** Defaults to "not offered" rather than throwing when no provider is mounted. */
export function useAskAssistant(): AskAssistantValue {
  return useContext(AskAssistantContext)
}
