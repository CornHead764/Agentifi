import { Plus } from 'lucide-react'
import { useState } from 'react'
import { Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom'

import { SeriesEditor } from '@/components/SeriesEditor'
import { Button, PageHeader, Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui'
import { formatDate } from '@/lib/format'
import { useAccounts, useCategories, useTags } from '@/lib/transactions/queries'
import { horizonOf, type Horizon } from '@/lib/upcoming/horizon'

import { AllSeriesTab } from './upcoming/AllSeriesTab'
import { CashFlowTab } from './upcoming/CashFlowTab'
import { HorizonPicker } from './upcoming/HorizonPicker'
import { OverviewTab } from './upcoming/OverviewTab'
import { RefundsTab } from './upcoming/RefundsTab'

/**
 * Bills & Income: four tabs over the recurring *series*, the single entity for
 * bills, subscriptions, income, transfers and card payments. There must be no
 * separate "bills" model. Suggested holds detected patterns awaiting a promote
 * step: suggest, never auto-create.
 *
 * Every write names a *slot* (a series id and a due date): a twice-monthly
 * bill has two occurrences a month, and marking "the series" paid would close
 * both with one charge.
 */
type TabKey = 'overview' | 'cash-flow' | 'series' | 'refunds'

const TAB_ROUTES: readonly { tab: TabKey; path: string }[] = [
  { tab: 'overview', path: '' },
  { tab: 'cash-flow', path: 'cash-flow' },
  { tab: 'series', path: 'recurring' },
  { tab: 'refunds', path: 'refund-tracker' },
]

function tabFromPath(segment: string | undefined): TabKey | null {
  if (segment === undefined || segment === '') return 'overview'
  return TAB_ROUTES.find((route) => route.path === segment)?.tab ?? null
}

export function UpcomingPage() {
  // `?new=1` opens the editor on arrival, for the links elsewhere that say
  // "add a recurring item".
  const [params, setParams] = useSearchParams()
  const [creating, setCreating] = useState<'series' | 'refund' | null>(() =>
    params.has('new') ? 'series' : null,
  )
  // One horizon for Overview and Cash flow, so switching tabs keeps the window.
  const [horizon, setHorizon] = useState<Horizon>(() => horizonOf(30))
  const accounts = useAccounts()
  const categories = useCategories()
  const tags = useTags()
  // The tab is the URL, as Simplifi routes it: /upcoming,
  // /upcoming/cash-flow, /upcoming/recurring, /upcoming/refund-tracker.
  const navigate = useNavigate()
  const tab = tabFromPath(useParams().tab)
  if (tab === null) {
    return <Navigate to="/upcoming" replace />
  }

  return (
    <div className="page page--wide">
      <Tabs
        value={tab}
        onValueChange={(next) => {
          const route = TAB_ROUTES.find((one) => one.tab === next)
          if (route === undefined) return
          navigate(route.path === '' ? '/upcoming' : `/upcoming/${route.path}`)
        }}
      >
        <PageHeader
          tabs={
            <TabsList>
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="cash-flow">Cash flow</TabsTrigger>
              <TabsTrigger value="series">Recurring</TabsTrigger>
              <TabsTrigger value="refunds">Refunds</TabsTrigger>
            </TabsList>
          }
          actions={
            tab === 'refunds' ? (
              <Button variant="primary" size="sm" onClick={() => setCreating('refund')}>
                <Plus size={13} /> Track a refund
              </Button>
            ) : (
              // "Series" is the entity's name, not the reader's; on screen it
              // is a recurring item, as the tab says.
              <Button variant="primary" size="sm" onClick={() => setCreating('series')}>
                <Plus size={13} /> New recurring item
              </Button>
            )
          }
        >
          {tab === 'overview' || tab === 'cash-flow' ? (
            <div className="upcoming__range">
              <HorizonPicker value={horizon} onChange={setHorizon} />
              <span className="muted">
                {formatDate(horizon.from)} – {formatDate(horizon.to)}
              </span>
            </div>
          ) : null}
        </PageHeader>

        <TabsContent value="overview">
          <OverviewTab horizon={horizon} />
        </TabsContent>
        <TabsContent value="cash-flow">
          <CashFlowTab horizon={horizon} />
        </TabsContent>
        <TabsContent value="series">
          <AllSeriesTab />
        </TabsContent>
        <TabsContent value="refunds">
          <RefundsTab />
        </TabsContent>
      </Tabs>

      <SeriesEditor
        open={creating !== null}
        onOpenChange={(open) => {
          if (!open) setCreating(null)
          if (!open && params.has('new')) {
            const next = new URLSearchParams(params)
            next.delete('new')
            setParams(next, { replace: true })
          }
        }}
        accounts={accounts.data ?? []}
        categories={categories.data ?? []}
        tags={creating === 'refund' ? [] : (tags.data ?? [])}
        fixedKind={creating === 'refund' ? 'refund' : undefined}
      />
    </div>
  )
}
