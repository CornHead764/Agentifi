import { useState } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import { Card, ChipGroup, PageHeader, SearchInput, Switch } from '@/components/ui'
import {
  SERIES_TABS,
  SERIES_TAB_LABELS,
  useSeriesList,
  useSuggestions,
  type SeriesTab,
} from '@/lib/clients/upcoming'

import { SeriesTable } from './SeriesTable'
import { SuggestionTable } from './SuggestionTable'
import { sortSeries } from '@/pages/settings/recurring/rows'

export function AllSeriesTab() {
  const [tab, setTab] = useState<SeriesTab>('all_active')
  const [search, setSearch] = useState('')
  const list = useSeriesList(tab, search)
  const [showDismissed, setShowDismissed] = useState(false)
  const suggestions = useSuggestions(tab === 'suggested' && !showDismissed)
  const dismissedSuggestions = useSuggestions(tab === 'suggested' && showDismissed, true)

  return (
    <Card flush>
      <PageHeader
        className="series__head"
        actions={
          <>
            {tab === 'suggested' ? (
              <Switch
                label="Show dismissed"
                labelPosition="before"
                checked={showDismissed}
                onCheckedChange={setShowDismissed}
              />
            ) : null}
            <SearchInput
              size="sm"
              placeholder="Search recurring"
              aria-label="Search recurring"
              value={search}
              onChange={setSearch}
            />
          </>
        }
      >
        <ChipGroup
          label="Recurring filters"
          layout="wrap"
          value={tab}
          options={SERIES_TABS.map((one) => ({ value: one, label: SERIES_TAB_LABELS[one] }))}
          onChange={setTab}
        />
      </PageHeader>

      {tab === 'suggested' ? (
        <QueryBoundary query={showDismissed ? dismissedSuggestions : suggestions} rows={10}>
          {(rows) => <SuggestionTable rows={rows} search={search} dismissed={showDismissed} />}
        </QueryBoundary>
      ) : (
        <QueryBoundary query={list} rows={10}>
          {(rows) => <SeriesTable rows={sortSeries(rows)} />}
        </QueryBoundary>
      )}
    </Card>
  )
}
