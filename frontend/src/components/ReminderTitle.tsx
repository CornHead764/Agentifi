import { ArrowUpRight } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { SeriesEditor } from '@/components/SeriesEditor'
import { Button } from '@/components/ui'
import { type Occurrence, useSeries } from '@/lib/clients/upcoming'
import { seriesPath } from '@/lib/transactions/reminders'
import { useAccounts, useCategories, useTags } from '@/lib/transactions/queries'

/**
 * A reminder's name. A series' reminder opens that recurring item's editor in
 * place, so the person stays on the screen they are on; a statement with no
 * series is plain text.
 */
export function ReminderTitle({ occurrence }: { occurrence: Occurrence }) {
  const [open, setOpen] = useState(false)
  if (occurrence.series_id === null) return <>{occurrence.label}</>

  return (
    <>
      <button
        type="button"
        className="reminder-card__open"
        aria-haspopup="dialog"
        aria-label={`Edit the recurring item ${occurrence.label}`}
        onClick={() => setOpen(true)}
      >
        {occurrence.label}
      </button>
      {open ? <ReminderSeriesEditor seriesId={occurrence.series_id} onClose={() => setOpen(false)} /> : null}
    </>
  )
}

/** Mounted only while open, so the series is fetched on the first click and never before. */
export function ReminderSeriesEditor({ seriesId, onClose }: { seriesId: string; onClose: () => void }) {
  const series = useSeries(seriesId)
  const accounts = useAccounts()
  const categories = useCategories()
  const tags = useTags()

  if (!series.data) return null

  return (
    <SeriesEditor
      open
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
      series={series.data}
      headerActions={
        <Button asChild variant="ghost" size="sm">
          <Link to={seriesPath(seriesId)} onClick={onClose}>
            <ArrowUpRight size={14} />
            Full page
          </Link>
        </Button>
      }
      accounts={accounts.data ?? []}
      categories={categories.data ?? []}
      tags={tags.data ?? []}
    />
  )
}
