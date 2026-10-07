import type { ConnectionTest } from '@/lib/clients/assistant'

/**
 * The probe's verdict, in one sentence somebody can act on. It names the
 * setting to change when the saved style is not the one that works.
 */
export function describeProbe(probe: ConnectionTest, saved: 'native' | 'prompted'): string {
  if (!probe.reachable) return `Could not reach the model: ${probe.detail}`
  if (probe.native_tool_calls) {
    return saved === 'native'
      ? 'Reachable, and the server returns tool calls natively. Nothing to change.'
      : 'Reachable, and the server returns tool calls natively — “Native” would be the faster setting.'
  }
  if (probe.prompted_tool_calls) {
    return saved === 'prompted'
      ? `Reachable. ${probe.detail}`
      : `Reachable, but the server drops native tool calls. Switch “Tool calls” to “In the prompt” and save: that style works with this server.`
  }
  return `Reachable, but neither style produced a tool call. ${probe.detail}`
}
