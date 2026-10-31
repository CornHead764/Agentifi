import {
  CalendarDays,
  ChartColumn,
  ChartPie,
  FileText,
  Pencil,
  Trash2,
  TrendingUp,
} from 'lucide-react'
import { useState } from 'react'

import { seriesColor } from '@/components/charts/palette'
import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Card,
  ConfirmDialog,
  EmptyState,
  List,
  ListRow,
  NameDialog,
  OverflowMenu,
  useConfirm,
} from '@/components/ui'
import {
  deleteSavedReport,
  renameSavedReport,
  useSavedReports,
  type SavedReport,
  reportKeys,
} from '@/lib/clients/reports'
import { useInvalidatingMutation } from '@/lib/queryClient'
import type { GalleryEntry, ReportPresetId } from '@/lib/reports/presets'

/** What each report draws, as a glyph in the report chart's categorical colour. */
const GLYPHS: Record<ReportPresetId, typeof ChartPie> = {
  spending: ChartColumn,
  income: ChartPie,
  income_summary: ChartColumn,
  income_expense: ChartColumn,
  net_worth: TrendingUp,
  savings: TrendingUp,
  taxes: FileText,
  monthly_summary: CalendarDays,
}

export function Gallery({
  gallery,
  onOpen,
  onOpenSaved,
}: {
  gallery: readonly GalleryEntry[]
  onOpen: (entry: GalleryEntry) => void
  onOpenSaved: (report: SavedReport) => void
}) {
  const saved = useSavedReports()
  const [renaming, setRenaming] = useState<SavedReport | null>(null)

  const removing = useConfirm(
    useInvalidatingMutation(deleteSavedReport, [reportKeys.saved], {
      failure: 'That report was not deleted',
    }),
    { variables: (report: SavedReport) => report.id },
  )

  // A name-only PATCH: the rail is not holding this report's knobs, and the
  // stored config must survive a rename untouched.
  const renamingReport = useInvalidatingMutation(
    ({ id, title }: { id: string; title: string }) => renameSavedReport(id, title),
    [reportKeys.saved],
    { failure: 'That report was not renamed' },
  )

  return (
    <div className="split split--gallery">
      <Card title="New report">
        <List className="gallery">
          {gallery.map((entry) => {
            const Glyph = GLYPHS[entry.id]
            return (
              <ListRow
                key={entry.id}
                title={
                  <span className="gallery__name">
                    <span
                      className="gallery__icon"
                      style={{ background: seriesColor(entry.series - 1) }}
                    >
                      <Glyph size={13} aria-hidden="true" />
                    </span>
                    {entry.name}
                  </span>
                }
                badge={entry.id === 'monthly_summary' ? <Badge>Snapshot</Badge> : undefined}
                sub={entry.blurb}
                onSelect={() => onOpen(entry)}
              />
            )
          })}
        </List>
      </Card>

      <Card title="Saved reports">
        <QueryBoundary
          query={saved}
          rows={4}
          empty={(rows) =>
            rows.length === 0 ? (
              <EmptyState
                title="No saved reports yet"
                body="Customized reports you save appear here."
              />
            ) : null
          }
        >
          {(rows) => (
            <List>
              {rows.map((report) => (
                <ListRow
                  key={report.id}
                  title={<span title={report.name}>{report.name}</span>}
                  onSelect={() => onOpenSaved(report)}
                  actions={
                    <OverflowMenu
                      label={`Actions for ${report.name}`}
                      actions={[
                        {
                          label: 'Rename…',
                          icon: <Pencil size={14} />,
                          onSelect: () => setRenaming(report),
                        },
                        {
                          label: 'Delete report',
                          icon: <Trash2 size={14} />,
                          danger: true,
                          disabled: removing.dialog.pending,
                          onSelect: () => removing.ask(report),
                        },
                      ]}
                    />
                  }
                />
              ))}
            </List>
          )}
        </QueryBoundary>
      </Card>

      <NameDialog
        open={renaming !== null}
        onOpenChange={(open) => (open ? undefined : setRenaming(null))}
        title="Rename report"
        label="Report name"
        initial={renaming?.name ?? ''}
        submitLabel="Rename"
        onSubmit={(title) =>
          renaming === null
            ? Promise.resolve()
            : renamingReport.mutateAsync({ id: renaming.id, title })
        }
      />

      <ConfirmDialog
        {...removing.dialog}
        title={`Delete ${removing.target?.name ?? 'this report'}?`}
        description="Removes the saved settings and filter. The ledger is unchanged."
        confirmLabel="Delete report"
      />
    </div>
  )
}
