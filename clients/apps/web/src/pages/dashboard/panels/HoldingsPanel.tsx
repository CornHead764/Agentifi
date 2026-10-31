import { PercentText } from '@/components/figures'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Badge, EmptyState, List, ListRow } from '@/components/ui'
import { usePortfolio } from '@/lib/clients/investments'

import { AddInvestmentAction } from './AddInvestmentAction'

/** Top movers today, ranked by how far each position moved. */
export function HoldingsPanel() {
  const portfolio = usePortfolio(null)
  return (
    <QueryBoundary query={portfolio} rows={5}>
      {(data) => {
        const movers = [...data.items]
          .filter((one) => one.day_change !== null)
          .sort((a, b) => Math.abs(b.day_change ?? 0) - Math.abs(a.day_change ?? 0))
          .slice(0, 10)

        // Two different silences: nothing held, and nothing that moved. A
        // blank card says neither.
        if (data.items.length === 0) {
          return (
            <EmptyState
              title="No holdings yet"
              body="Positions appear here once an investment account syncs or a holding is entered."
              action={<AddInvestmentAction />}
            />
          )
        }
        if (movers.length === 0) {
          return (
            <EmptyState
              title="No moves today"
              body="Nothing held has a price change on file for today."
            />
          )
        }

        return (
          <List>
            {movers.map((mover) => (
              <ListRow
                key={mover.id}
                title={<Badge>{mover.symbol}</Badge>}
                sub={mover.name}
                figures={<Money value={mover.market_value} tone="neutral" />}
                figuresSub={<PercentText rate={mover.day_change_pct} showPlus />}
              />
            ))}
          </List>
        )
      }}
    </QueryBoundary>
  )
}
