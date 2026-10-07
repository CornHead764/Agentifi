import { Settings2 } from 'lucide-react'
import { useState, type ReactNode } from 'react'

import { ErrorBoundary } from '@/components/ErrorBoundary'
import { GetStarted } from '@/components/onboarding/GetStarted'
import { SetupGuide } from '@/components/onboarding/SetupGuide'
import { Button, Card, PageHeader } from '@/components/ui'
import { useVisibleSetupGuide } from '@/lib/clients/setupGuide'
import { formatDate, plural, toIsoDate } from '@/lib/format'
import { reviewQueueLink } from '@/lib/transactions/links'
import { useAccounts } from '@/lib/transactions/queries'

import { CustomizeDrawer } from './dashboard/CustomizeDrawer'
import {
  WIDGET_SPANS,
  WIDGET_TITLES,
  useDashboardLayout,
  type WidgetId,
  type WidgetLayout,
} from './dashboard/layout'
import {
  BillsPanel,
  GoalsPanel,
  HoldingsPanel,
  InvestmentsPanel,
  MonthlyBarsPanel,
  NetWorthPanel,
  PlannedSpendPanel,
  RecentTransactionsPanel,
  ReviewPanel,
  SavingsRatePanel,
  SpendingPlanPanel,
  TopCategoriesPanel,
  WatchlistPanel,
  WidgetLink,
} from './dashboard/panels'

/** Where each widget's own page lives, for the header link. */
const WIDGET_LINKS: Partial<Record<WidgetId, { to: string; label: string }>> = {
  recent: { to: '/transactions', label: 'See all' },
  review: { to: reviewQueueLink(), label: 'Review all' },
  net_worth: { to: '/net-worth', label: 'Net worth' },
  bills: { to: '/upcoming', label: 'Bills & Income' },
  income: { to: '/reports', label: 'Income report' },
  spending: { to: '/reports', label: 'Spending report' },
  top_categories: { to: '/reports', label: 'Reports' },
  spending_plan: { to: '/spending-plan', label: 'Spending Plan' },
  planned_spend: { to: '/spending-plan/planned-spending', label: 'Planned Spend' },
  goals: { to: '/goals', label: 'Savings Goals' },
  watchlist: { to: '/watchlist', label: 'Watchlists' },
  holdings: { to: '/investing', label: 'Investments' },
  investments: { to: '/investing', label: 'Investments' },
}

const WIDGET_SUBTITLES: Partial<Record<WidgetId, string>> = {
  income: 'Recent 6 months',
  spending: 'Recent 6 months',
  top_categories: 'This month',
  holdings: 'Top 10 movers today',
}

/**
 * The dashboard: a user-ordered widget grid, each widget a compressed read of
 * a page that exists elsewhere. Each panel queries the endpoint its page does,
 * so a figure cannot drift from its page, and a switched-off widget is not
 * rendered at all, so it fetches nothing.
 */
export function DashboardPage() {
  const { layout, apply } = useDashboardLayout()
  const [customizing, setCustomizing] = useState(false)
  // For the drawer's account checklist, and to tell an empty space from a quiet
  // month. The same query the panels read, so it costs nothing extra.
  const accounts = useAccounts()
  const guide = useVisibleSetupGuide()
  const hidden = layout.filter((entry) => !entry.on).length
  const empty = accounts.isSuccess && accounts.data.length === 0

  if (empty) {
    return (
      <div className="page">
        <GetStarted />
      </div>
    )
  }

  return (
    <div className="page">
      {/* Today's date, not a greeting: every panel below reports a window. */}
      <PageHeader
        title={formatDate(toIsoDate(new Date()), 'full')}
        actions={
          <Button size="sm" onClick={() => setCustomizing(true)}>
            <Settings2 size={13} /> Customize
          </Button>
        }
      />

      {guide ? <SetupGuide guide={guide} /> : null}

      <div className="dash">
        {layout
          .filter((entry) => entry.on)
          .map((entry) => (
            <Widget key={entry.id} id={entry.id}>
              {panelFor(entry)}
            </Widget>
          ))}
      </div>

      {/* Nine panels are off to begin with, and a page that simply does not
          draw them would read as a page that does not have them. */}
      {hidden > 0 ? (
        <p className="dash__hidden muted">
          {plural(hidden, 'more panel')} hidden.{' '}
          <Button size="sm" variant="ghost" onClick={() => setCustomizing(true)}>
            Add panels
          </Button>
        </p>
      ) : null}

      <CustomizeDrawer
        open={customizing}
        onOpenChange={setCustomizing}
        layout={layout}
        accounts={accounts.data ?? []}
        onApply={apply}
      />
    </div>
  )
}

function Widget({ id, children }: { id: WidgetId; children: ReactNode }) {
  const link = WIDGET_LINKS[id]
  return (
    <Card
      className={WIDGET_SPANS[id] === 2 ? 'widget widget--wide' : 'widget'}
      title={WIDGET_TITLES[id]}
      subtitle={WIDGET_SUBTITLES[id]}
      actions={link ? <WidgetLink to={link.to}>{link.label}</WidgetLink> : undefined}
    >
      <ErrorBoundary scope="panel">{children}</ErrorBoundary>
    </Card>
  )
}

function panelFor({ id, accounts }: WidgetLayout): ReactNode {
  switch (id) {
    case 'recent':
      return <RecentTransactionsPanel accounts={accounts} />
    case 'review':
      return <ReviewPanel />
    case 'net_worth':
      return <NetWorthPanel />
    case 'bills':
      return <BillsPanel />
    case 'income':
      return <MonthlyBarsPanel direction="income" />
    case 'spending':
      return <MonthlyBarsPanel direction="spending" />
    case 'savings_rate':
      return <SavingsRatePanel />
    case 'top_categories':
      return <TopCategoriesPanel />
    case 'spending_plan':
      return <SpendingPlanPanel />
    case 'planned_spend':
      return <PlannedSpendPanel />
    case 'goals':
      return <GoalsPanel />
    case 'watchlist':
      return <WatchlistPanel />
    case 'holdings':
      return <HoldingsPanel />
    case 'investments':
      return <InvestmentsPanel />
  }
}
