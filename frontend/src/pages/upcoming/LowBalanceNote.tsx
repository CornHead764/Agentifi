import { clsx } from 'clsx'

import { Money } from '@/components/Money'
import type { CashFlowLine } from '@/lib/clients/upcoming'
import { formatDate } from '@/lib/format'

/**
 * One line's worst day. `first_below` is the server's threshold crossing,
 * never recomputed here from the points held, which would disagree with the
 * lines when the window or account set moved.
 */
export function LowBalanceNote({ line, warned }: { line: CashFlowLine; warned: boolean }) {
  if (line.lowest === null) return null
  return (
    <span
      className={clsx('acct-tree__warning', line.first_below !== null && 'acct-tree__warning--below')}
    >
      {line.first_below === null ? null : (
        <>Dips below on {formatDate(line.first_below.on, 'short')} · </>
      )}
      {warned && line.first_below === null ? 'Stays above · ' : null}
      Lowest <Money value={line.lowest.balance} /> on {formatDate(line.lowest.on, 'short')}
    </span>
  )
}
