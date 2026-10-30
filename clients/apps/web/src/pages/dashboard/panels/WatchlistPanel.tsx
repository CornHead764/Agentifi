import { Link } from 'react-router-dom'

import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import { Button, Card, EmptyState } from '@/components/ui'
import { useWatchlists } from '@/lib/clients/dashboard'
import type { Money as MoneyValue } from '@/lib/money'
import { averageOfFullMonths } from '@/lib/watchlists'

import { WatchlistTarget } from '../../watchlist/WatchlistTarget'

export function WatchlistPanel() {
  const watchlists = useWatchlists()
  return (
    <QueryBoundary query={watchlists} rows={3}>
      {(rows) =>
        rows.length === 0 ? (
          <EmptyState
            title="No watchlists yet"
            body="Track spending in one category, payee or tag."
            action={
              <Button asChild variant="primary" size="sm">
                <Link to="/watchlist">New watchlist</Link>
              </Button>
            }
          />
        ) : (
          <div className="cards">
            {rows.slice(0, 2).map((watchlist) => (
              <Card
                key={watchlist.id}
                title={
                  <>
                    {watchlist.emoji ? <span aria-hidden="true">{watchlist.emoji}</span> : null}{' '}
                    {watchlist.name}
                  </>
                }
              >
                <div className="stack stack--2">
                  <span className="watch__figures">
                    <Figure
                      label="12 month"
                      caption="Monthly average"
                      value={averageOfFullMonths(watchlist.monthly_trend)}
                    />
                    <Figure
                      label="Year to date"
                      caption="This year"
                      value={watchlist.year_to_date}
                    />
                    <Figure
                      label="This month"
                      caption="Spent so far"
                      value={watchlist.this_month_spent}
                    />
                    <Figure
                      label="Projected"
                      caption="This month"
                      value={watchlist.month_projection}
                    />
                  </span>
                  <WatchlistTarget watchlist={watchlist} />
                </div>
              </Card>
            ))}
          </div>
        )
      }
    </QueryBoundary>
  )
}

function Figure({
  label,
  caption,
  value,
}: {
  label: string
  caption: string
  value: MoneyValue
}) {
  return (
    <span className="watch__figure">
      <span className="muted">{label}</span>
      <Money value={value} signs="absolute" tone="neutral" />
      <span className="muted">{caption}</span>
    </span>
  )
}
