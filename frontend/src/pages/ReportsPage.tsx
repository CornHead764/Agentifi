import { Home, X } from 'lucide-react'
import { useContext, useEffect, useMemo, useState, type ReactNode } from 'react'

import { IconButton, PageHeader, Tabs, TabsList, TabsTrigger } from '@/components/ui'
import { AuthContext } from '@/contexts/auth'
import {
  useReportPresets,
  useSavedReports,
  type ReportConfig,
  type SavedReport,
} from '@/lib/clients/reports'
import { useCurrentSpace } from '@/lib/clients/spaces'
import {
  ALL_TIME_SELECTION,
  asRangePreset,
  selectionForToken,
  tokenForRangePreset,
} from '@/lib/dateRanges'
import { mergeGallery, type GalleryEntry } from '@/lib/reports/presets'
import { splitSavedSearch } from '@/lib/reports/savedFilter'
import {
  HOME_TAB,
  isDirty,
  openTab,
  pruneDeleted,
  readWorkspace,
  workspaceKey,
  writeWorkspace,
  type ReportTab,
  type TabState,
} from '@/lib/reports/tabs'
import { useCategories, useTags } from '@/lib/transactions/queries'

import { Gallery } from './reports/Gallery'
import { ReportWorkspace } from './reports/ReportWorkspace'

/**
 * Reports: a tabbed workspace over one engine. A tab's title carries a dot
 * once its knobs or filter differ from what the server has.
 *
 * Switching Report type between Transaction and Summary changes only `mode`,
 * and the engine answers with the other half of the same result, so the two
 * renderings cannot disagree about a subtotal.
 */
export function ReportsPage() {
  // Through the context rather than `useAuth`, which throws without a
  // provider: the test harness renders this page too. The page only mounts
  // behind `RequireAuth`, so the id is settled before the first read.
  const auth = useContext(AuthContext)
  const key = workspaceKey(auth?.user?.id)
  const [restored] = useState(() => readWorkspace(key))
  const [tabs, setTabs] = useState<ReportTab[]>(restored.tabs)
  const [active, setActive] = useState(restored.active)

  const savedReports = useSavedReports()

  // A tab whose report was deleted (here, or by another member)
  // stops being a saved one, or its Save would write a row nobody can find.
  // Derived rather than pruned in an effect, which would render per refetch.
  const openTabs = useMemo(
    () => pruneDeleted(tabs, savedReports.data ?? null),
    [tabs, savedReports.data],
  )

  useEffect(() => {
    writeWorkspace(key, { tabs: openTabs, active })
  }, [key, openTabs, active])

  const presets = useReportPresets()
  const gallery = useMemo(() => mergeGallery(presets.data), [presets.data])

  // The space's default range opens a *new* tab only; a restored tab keeps the
  // window it was left on.
  const { data: space } = useCurrentSpace()
  const preset = asRangePreset(space?.default_date_range)
  const preferred = preset === null ? null : tokenForRangePreset(preset)

  const categories = useCategories()
  const tags = useTags()
  const universe = useMemo(
    () => ({ categories: categories.data ?? [], tags: tags.data ?? [] }),
    [categories.data, tags.data],
  )

  const open = (entry: GalleryEntry) => {
    const tab = openTab(preferred === null ? entry : { ...entry, range: preferred })
    setTabs((current) => [...current, tab])
    setActive(tab.id)
  }

  const openSaved = (report: SavedReport) => {
    const existing = openTabs.find((tab) => tab.savedId === report.id)
    if (existing) {
      setActive(existing.id)
      return
    }
    const entry = gallery.find((one) => one.id === report.config.preset) ?? gallery[0]
    // The box owns the text item it wrote, so it comes back to the box rather
    // than to the panel as a facet nobody ticked.
    const opened = splitSavedSearch(report.filter.items, report.filter.query_text, universe)
    const tab = openTab(
      entry,
      {
        range: selectionForToken(preferred ?? entry.range) ?? ALL_TIME_SELECTION,
        config: report.config,
        filter: opened.filter,
        search: opened.search,
      },
      report,
    )
    setTabs((current) => [...current, tab])
    setActive(tab.id)
  }

  const close = (id: string) => {
    setTabs((current) => current.filter((tab) => tab.id !== id))
    setActive((current) => (current === id ? HOME_TAB : current))
  }

  const update = (id: string, next: Partial<TabState>) => {
    setTabs((current) =>
      current.map((tab) => (tab.id === id ? { ...tab, state: { ...tab.state, ...next } } : tab)),
    )
  }

  const patchConfig = (id: string, next: Partial<ReportConfig>) => {
    setTabs((current) =>
      current.map((tab) =>
        tab.id === id ? { ...tab, state: { ...tab.state, config: { ...tab.state.config, ...next } } } : tab,
      ),
    )
  }

  const reset = (id: string) => {
    setTabs((current) =>
      current.map((tab) => (tab.id === id ? { ...tab, state: tab.baseline } : tab)),
    )
  }

  const saved = (tab: ReportTab, row: SavedReport) => {
    setTabs((current) =>
      current.map((one) =>
        // The baseline becomes the state that was *sent*, not the state now: an
        // edit made while the request was in flight is still unsaved.
        one.id === tab.id ? { ...one, savedId: row.id, title: row.name, baseline: tab.state } : one,
      ),
    )
  }

  const current = openTabs.find((tab) => tab.id === active)

  // The strip is the page header's, so a report's own range and Save sit on
  // the same row as the tabs.
  const header = ({ children, actions }: { children?: ReactNode; actions?: ReactNode }) => (
    <Tabs value={current ? current.id : HOME_TAB} onValueChange={setActive}>
      <PageHeader
        className="reports__header"
        tabs={
          <TabsList aria-label="Open reports">
            <TabsTrigger value={HOME_TAB}>
              <Home size={13} aria-hidden="true" /> Home
            </TabsTrigger>
            {openTabs.map((tab) => (
              <span key={tab.id} className="report-tab">
                <TabsTrigger value={tab.id}>
                  {tab.title}
                  {isDirty(tab) ? <span className="report-tab__dot" title="Unsaved edits" /> : null}
                </TabsTrigger>
                <IconButton
                  label={`Close ${tab.title}`}
                  variant="ghost"
                  size="sm"
                  onClick={() => close(tab.id)}
                >
                  <X size={12} />
                </IconButton>
              </span>
            ))}
          </TabsList>
        }
        actions={actions}
      >
        {children}
      </PageHeader>
    </Tabs>
  )

  return (
    <div className="page page--wide">
      {current ? (
        <ReportWorkspace
            // Keyed by the tab: the workspace holds the search debounce, and a
            // new tab must not run against the old one's pending text.
          key={current.id}
          tab={current}
          universe={universe}
          header={header}
          onUpdate={(next) => update(current.id, next)}
          onConfig={(next) => patchConfig(current.id, next)}
          onReset={() => reset(current.id)}
          onSaved={(row) => saved(current, row)}
        />
      ) : (
        <>
          {header({})}
          <Gallery gallery={gallery} onOpen={open} onOpenSaved={openSaved} />
        </>
      )}
    </div>
  )
}
