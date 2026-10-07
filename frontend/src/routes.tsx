import { lazy, Suspense, type ComponentType } from 'react'
import { Navigate, Route, Routes, useLocation, useParams } from 'react-router-dom'

import { AppShell } from '@/components/AppShell'
import { RequireAuth, RequireOwnPassword } from '@/components/RequireAuth'
import { ALL_MERCHANTS, MERCHANTS, type MerchantId } from '@/lib/merchants'

/** One chunk per page; the name picks the named export out of its module. */
function page<K extends string>(
  load: () => Promise<Record<K, ComponentType>>,
  name: K,
): ComponentType {
  return lazy(async () => {
    const component: ComponentType = (await load())[name]
    return { default: component }
  })
}

const LoginPage = page(() => import('@/pages/LoginPage'), 'LoginPage')
const OidcCallbackPage = page(() => import('@/pages/OidcCallbackPage'), 'OidcCallbackPage')
const SetPasswordPage = page(() => import('@/pages/SetPasswordPage'), 'SetPasswordPage')
const DashboardPage = page(() => import('@/pages/DashboardPage'), 'DashboardPage')
const GoalsPage = page(() => import('@/pages/GoalsPage'), 'GoalsPage')
const InvestingPage = page(() => import('@/pages/InvestingPage'), 'InvestingPage')
const NetWorthPage = page(() => import('@/pages/NetWorthPage'), 'NetWorthPage')
const NotFoundPage = page(() => import('@/pages/NotFoundPage'), 'NotFoundPage')
const PlanningToolsPage = page(() => import('@/pages/PlanningToolsPage'), 'PlanningToolsPage')
const ReportsPage = page(() => import('@/pages/ReportsPage'), 'ReportsPage')
const RulesPage = page(() => import('@/pages/RulesPage'), 'RulesPage')
const SettingsPage = page(() => import('@/pages/SettingsPage'), 'SettingsPage')
const SpendingPlanPage = page(() => import('@/pages/SpendingPlanPage'), 'SpendingPlanPage')
const TransactionsPage = page(() => import('@/pages/TransactionsPage'), 'TransactionsPage')
const UpcomingPage = page(() => import('@/pages/UpcomingPage'), 'UpcomingPage')
const AssistantPage = page(() => import('@/pages/AssistantPage'), 'AssistantPage')
const WatchlistPage = page(() => import('@/pages/WatchlistPage'), 'WatchlistPage')
const GeneralSettings = page(() => import('@/pages/settings/GeneralSettings'), 'GeneralSettings')
const SecuritySettings = page(() => import('@/pages/settings/SecuritySettings'), 'SecuritySettings')
const AccountsSettings = page(() => import('@/pages/settings/AccountsSettings'), 'AccountsSettings')
const AdminSettings = page(() => import('@/pages/settings/AdminSettings'), 'AdminSettings')
const BillsSettings = page(() => import('@/pages/settings/BillsSettings'), 'BillsSettings')
const EmailSettings = page(() => import('@/pages/settings/EmailSettings'), 'EmailSettings')
const CategoriesTagsSettings = page(() => import('@/pages/settings/CategoriesTagsSettings'), 'CategoriesTagsSettings')
const NotificationsSettings = page(() => import('@/pages/settings/NotificationsSettings'), 'NotificationsSettings')
const SpacesSettings = page(() => import('@/pages/settings/SpacesSettings'), 'SpacesSettings')
const TransfersSettings = page(() => import('@/pages/settings/TransfersSettings'), 'TransfersSettings')
const MerchantsSettings = page(() => import('@/pages/settings/MerchantsSettings'), 'MerchantsSettings')

/**
 * `RequireAuth` and `RequireOwnPassword` guard structurally, so no page can
 * forget them. An account register is `/transactions?displayNode=`, not a
 * route of its own.
 */
export function AppRoutes() {
  return (
    <Suspense fallback={null}>
      <Routes>
        <Route path="login" element={<LoginPage />} />
        <Route path="auth/oidc/callback" element={<OidcCallbackPage />} />

        <Route element={<RequireAuth />}>
          {/* Outside the shell on purpose: while a password change is owed the
              API serves nothing the shell could draw. */}
          <Route path="set-password" element={<SetPasswordPage />} />

          <Route element={<RequireOwnPassword />}>
            <Route element={<AppShell />}>
              <Route index element={<DashboardPage />} />
              <Route path="transactions" element={<TransactionsPage />} />
              <Route path="net-worth" element={<NetWorthPage />} />
              <Route path="spending-plan" element={<SpendingPlanPage />} />
              <Route path="spending-plan/:bucket" element={<SpendingPlanPage />} />
              <Route path="goals" element={<GoalsPage />} />
              <Route path="upcoming" element={<UpcomingPage />} />
              <Route path="upcoming/:tab" element={<UpcomingPage />} />
              {/* Bills & Income lives at /upcoming; /bills also answers. */}
              <Route path="bills" element={<Navigate to="/upcoming" replace />} />
              <Route path="bills/:tab" element={<BillsTabAlias />} />
              <Route path="planning-tools" element={<PlanningToolsPage />} />
              <Route path="investing" element={<InvestingPage />} />
              <Route path="watchlist" element={<WatchlistPage />} />
              <Route path="watchlist/:watchlistId" element={<WatchlistPage />} />
              <Route path="assistant" element={<AssistantPage />} />
              <Route path="rules" element={<RulesPage />} />
              <Route path="rules/:tab" element={<RulesPage />} />
              <Route path="reports" element={<ReportsPage />} />

              <Route path="settings" element={<SettingsPage />}>
                <Route index element={<Navigate to="general" replace />} />
                <Route path="general" element={<GeneralSettings />} />
                <Route path="accounts" element={<AccountsSettings />} />
                <Route path="admin" element={<AdminSettings />} />
                <Route path="bills" element={<BillsSettings />} />
                <Route path="categories-tags" element={<CategoriesTagsSettings />} />
                <Route path="email" element={<EmailSettings />} />
                <Route path="merchants" element={<MerchantsSettings />} />
                <Route path="merchants/:merchant" element={<MerchantsSettings />} />
                {/* `/settings/<merchant>` is an alias of its section. */}
                {ALL_MERCHANTS.map((merchant) => (
                  <Route
                    key={merchant}
                    path={merchant}
                    element={<MerchantSectionAlias merchant={merchant} />}
                  />
                ))}
                <Route path="notifications" element={<NotificationsSettings />} />
                {/* Aliases of pages that live outside Settings. */}
                <Route path="assistant" element={<Navigate to="/assistant" replace />} />
                <Route path="recurring" element={<Navigate to="/upcoming/recurring" replace />} />
                <Route path="rules" element={<Navigate to="/rules" replace />} />
                <Route path="spaces" element={<SpacesSettings />} />
                <Route path="transfers" element={<TransfersSettings />} />
                <Route path="security" element={<SecuritySettings />} />
              </Route>

              <Route path="*" element={<NotFoundPage />} />
            </Route>
          </Route>
        </Route>
      </Routes>
    </Suspense>
  )
}

/** `/settings/<merchant>`, redirected with whatever the fragment carried intact. */
function MerchantSectionAlias({ merchant }: { merchant: MerchantId }) {
  const { hash, search } = useLocation()
  return <Navigate to={{ pathname: MERCHANTS[merchant].settingsPath, search, hash }} replace />
}

/** `/bills/cash-flow` lands on the same tab under the page's own path. */
function BillsTabAlias() {
  const { tab } = useParams()
  return <Navigate to={`/upcoming/${tab ?? ''}`} replace />
}
