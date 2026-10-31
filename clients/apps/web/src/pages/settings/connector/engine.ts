/**
 * Whether the server can sign in to anything, read into one shape from the bill
 * engine's and the merchant engine's different answers.
 */

import type { BillAgentStatus } from '@/lib/clients/bills'
import type { MerchantAgentStatus } from '@/lib/clients/merchant'

export type EngineState =
  | { kind: 'checking' }
  | { kind: 'ready' }
  | { kind: 'absent' }
  | { kind: 'unavailable'; detail: string }
  | { kind: 'down'; detail: string }

export function billEngine(agent: BillAgentStatus | undefined): EngineState {
  if (agent === undefined) return { kind: 'checking' }
  if (!agent.configured) return { kind: 'absent' }
  if (!agent.healthy) return { kind: 'down', detail: agent.error }
  return { kind: 'ready' }
}

export function merchantEngine(agent: MerchantAgentStatus | undefined): EngineState {
  if (agent === undefined) return { kind: 'checking' }
  if (!agent.configured) return { kind: 'absent' }
  if (agent.unavailable !== '') return { kind: 'unavailable', detail: agent.unavailable }
  if (!agent.reachable) return { kind: 'down', detail: agent.detail }
  return { kind: 'ready' }
}

/** Whether signing in can be offered: a down engine may come back, an absent one cannot. */
export function engineSignsIn(state: EngineState): boolean {
  return state.kind !== 'absent' && state.kind !== 'unavailable'
}

/** The sentence for a state that stops signing in; still asking says nothing. */
export function engineSentence(state: EngineState): string | null {
  switch (state.kind) {
    case 'checking':
    case 'ready':
      return null
    case 'absent':
      return 'This build carries no browser engine, so signing in is off.'
    case 'unavailable':
      return state.detail
    case 'down':
      return state.detail.trim() === ''
        ? 'The browser engine is here but not answering.'
        : `The browser engine is here but not answering: ${state.detail.trim()}`
  }
}
