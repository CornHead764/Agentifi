import { seriesColor } from '@/components/charts/palette'
import type { CashFlowLine } from '@/lib/clients/upcoming'

/**
 * One colour per account, by its place in the whole projection: the line and
 * the account tree's dot read the same map, so unticking an account or
 * grouping the tree never recolours another one.
 */
export function accountColors(lines: readonly CashFlowLine[]): (accountId: string) => string {
  const index = new Map(lines.map((line, at) => [line.account_id, at]))
  return (accountId) => seriesColor(index.get(accountId) ?? 0)
}
