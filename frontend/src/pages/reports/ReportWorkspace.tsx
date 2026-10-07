import { RotateCcw } from 'lucide-react'
import { useMemo, type ReactNode } from 'react'

import { DateRangeControl } from '@/components/transactions/DateRangeControl'
import { FilterPanel } from '@/components/transactions/FilterPanel'
import { Button, SearchInput } from '@/components/ui'
import {
  type ReportConfig,
  type ReportQuery,
  type SavedReport,
} from '@/lib/clients/reports'
import { windowOf } from '@/lib/dateRanges'
import { galleryEntry } from '@/lib/reports/presets'
import { isDirty, type ReportTab, type TabState } from '@/lib/reports/tabs'
import { toFilterItems, type FilterUniverse } from '@/lib/transactions/filter'
import { useAccounts, useAdHocFilter } from '@/lib/transactions/queries'
import { useSettled } from '@/lib/useSettled'

import { EngineReport } from './EngineReport'
import { Knobs } from './Knobs'
import { MonthlySnapshot } from './MonthlySnapshot'
import { NetWorthReport } from './NetWorthReport'
import { SaveButton } from './SaveButton'
import { SavingsReport } from './SavingsReport'
import { SpendingReport } from './SpendingReport'

interface WorkspaceProps {
  tab: ReportTab
  universe: FilterUniverse
  /** The page's header row, which holds the open tabs. */
  header: (slots: { children?: ReactNode; actions?: ReactNode }) => ReactNode
  onUpdate: (next: Partial<TabState>) => void
  onConfig: (next: Partial<ReportConfig>) => void
  onReset: () => void
  onSaved: (row: SavedReport) => void
}

/** One open report. Spending reads periods rather than a date range, so it draws its own header controls. */
export function ReportWorkspace(props: WorkspaceProps) {
  if (galleryEntry(props.tab.presetId).id === 'spending') return <SpendingReport {...props} />
  return <RangeWorkspace {...props} />
}

function RangeWorkspace({ tab, universe, header, onUpdate, onConfig, onReset, onSaved }: WorkspaceProps) {
  const entry = galleryEntry(tab.presetId)
  const accounts = useAccounts()
  const bounds = useMemo(() => windowOf(tab.state.range), [tab.state.range])

  // `/reports/run` takes a `filter_id` and cannot carry items inline, so one
  // ad-hoc filter row is written and rewritten in place, as the register does.
  // The typed text is a `text` filter item like any other facet; the engine
  // has no search parameter. Debounced, because every change rewrites that
  // row; the box itself is not, since the tab holds the text.
  const search = useSettled(tab.state.search).trim()
  const items = useMemo(
    () =>
      toFilterItems(
        search === '' ? tab.state.filter : { ...tab.state.filter, texts: [...tab.state.filter.texts, search] },
        universe,
      ),
    [search, tab.state.filter, universe],
  )
  const adHoc = useAdHocFilter(items, search)

  const query: ReportQuery = {
    from: bounds.from,
    to: bounds.to,
    config: tab.state.config,
    filterId: adHoc.filterId,
  }

  return (
    <>
      {header({
        children: (
          <DateRangeControl value={tab.state.range} onChange={(range) => onUpdate({ range })} />
        ),
        actions: (
          <>
            {isDirty(tab) ? (
              <Button size="sm" onClick={onReset}>
                <RotateCcw size={13} /> Reset report
              </Button>
            ) : null}
            <SaveButton tab={tab} items={items} queryText={search} onSaved={onSaved} />
          </>
        ),
      })}

      {entry.id === 'net_worth' ? (
        <NetWorthReport from={bounds.from} to={bounds.to} />
      ) : entry.id === 'savings' ? (
        <SavingsReport from={bounds.from} to={bounds.to} />
      ) : entry.id === 'monthly_summary' ? (
        <MonthlySnapshot month={bounds.to.slice(0, 7)} />
      ) : (
        <EngineReport
          name={tab.title}
          query={query}
          enabled={!adHoc.pending}
          filterFailed={Boolean(adHoc.error)}
          toolbar={
            <div className="toolbar toolbar--flush toolbar--pack">
              <SearchInput
                size="sm"
                value={tab.state.search}
                placeholder="Search payees and notes"
                aria-label="Search this report"
                onChange={(next) => onUpdate({ search: next })}
              />
              <FilterPanel
                draft={tab.state.filter}
                categories={universe.categories}
                tags={universe.tags}
                accounts={accounts.data ?? []}
                payees={[]}
                onApply={(filter) => onUpdate({ filter })}
              />
              <span className="toolbar__spacer" />
              <Knobs config={tab.state.config} onConfig={onConfig} />
            </div>
          }
        />
      )}
    </>
  )
}
