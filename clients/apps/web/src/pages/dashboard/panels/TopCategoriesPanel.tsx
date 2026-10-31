import { Link, useNavigate } from 'react-router-dom'

import { Donut, DonutGroup, DonutLegend, type DonutSlice } from '@/components/charts'
import { seriesColor } from '@/components/charts/palette'
import { QueryBoundary } from '@/components/QueryBoundary'
import { useTopCategories } from '@/lib/clients/dashboard'
import { registerLinkForWindow, reviewQueueLink } from '@/lib/transactions/links'

export function TopCategoriesPanel() {
  const report = useTopCategories()
  const navigate = useNavigate()
  return (
    <QueryBoundary query={report} rows={5}>
      {(data) => {
        const rows = data.summary?.rows ?? []
        const ranked = [...rows].sort((a, b) => Math.abs(b.total) - Math.abs(a.total)).slice(0, 6)
        // The server keys the uncategorized row with an empty string; when it
        // is the largest wedge, the panel says so under the chart.
        const unfiled = rows.find((row) => row.key === '')
        const spent = rows.reduce((sum, row) => sum + Math.abs(row.total), 0)
        const dominated =
          unfiled !== undefined && spent > 0 && Math.abs(unfiled.total) / spent >= 0.2
        // The window the figures were taken over, so the register opens on the
        // same month the panel is reporting (trap 5).
        const window = { from: data.window.from, to: data.window.to }
        const slices: DonutSlice[] = ranked.map((row, index) => ({
          key: row.key,
          label: row.label,
          value: row.total,
          color: seriesColor(index),
          action: `Show ${row.label} in the register`,
        }))
        return (
          <>
            <DonutGroup
              className="donut-row donut-row--tight"
              onSelect={(slice) => navigate(registerLinkForWindow(window, { categoryId: slice.key }))}
            >
              <Donut height={150} signs="absolute" slices={slices} />
              <DonutLegend signs="absolute" slices={slices} />
            </DonutGroup>
            {dominated ? (
              <p className="muted dash__note">
                Mostly uncategorized.{' '}
                <Link to={reviewQueueLink({ uncategorized: true })}>Categorize</Link>
              </p>
            ) : null}
          </>
        )
      }}
    </QueryBoundary>
  )
}
