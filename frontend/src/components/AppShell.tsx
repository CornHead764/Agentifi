import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'

import { AskAssistantProvider } from '@/components/assistant/AskAssistantProvider'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import { FailureScreenshotDialog } from '@/components/FailureScreenshot'
import { CONNECT_PATH } from '@/components/onboarding/paths'
import { NoAccountsNotice } from '@/components/onboarding/NoAccountsNotice'
import { AccountsDrawer, type AccountsDrawerProps } from '@/components/shell/AccountsDrawer'
import { AppHeader, type ShellNotification } from '@/components/shell/AppHeader'
import { ConnectedAccountsDrawer } from '@/components/shell/ConnectedAccountsDrawer'
import { initialsFor } from '@/components/shell/initials'
import { NavRail } from '@/components/shell/NavRail'
import { railStartsCollapsed, storeRailCollapsed } from '@/components/shell/railCollapse'
import { NoSpaceGate } from '@/components/shell/NoSpaceGate'
import { PendingInvitations } from '@/components/shell/PendingInvitations'
import { PullToRefresh } from '@/components/shell/PullToRefresh'
import { showsAccountsDrawer, titleForPath } from '@/components/shell/destinations'
import { TabBar } from '@/components/shell/TabBar'
import { SignInDialogs, SignInToasts } from '@/components/signin/SignInHost'
import { SignInTasksProvider } from '@/components/signin/SignInTasksProvider'
import { SkeletonRows, TooltipProvider, useToastOrNull } from '@/components/ui'
import { syncEndedToast, syncProgressShort } from '@/components/syncText'
import { useBrowserLayoutEffect } from '@/components/ui/overflow-edges'
import { useAuth } from '@/contexts/auth'
import { useActiveSpace } from '@/contexts/space'
import { HeaderOverrideProvider } from '@/contexts/HeaderOverrideProvider'
import { useHeaderOverrideNode } from '@/contexts/headerOverride'
import {
  isSyncing,
  useConnections,
  useSyncConnection,
  useSyncEnded,
  type Connection,
} from '@/lib/clients/connections'
import { useCurrentSpace, useSpaces } from '@/lib/clients/spaces'
import {
  useClearAllNotifications,
  useClearNotification,
  useMarkAllNotificationsRead,
  useMarkNotificationRead,
  useNotificationFeed,
  type NotificationCard,
} from '@/lib/clients/notifications'
import { displayLocale, setDisplayLocale } from '@/lib/locale'
import { setDisplayCurrency } from '@/lib/money'
import { motionIsOff, restartArrival } from '@/lib/pageArrival'
import { useMediaQuery } from '@/lib/useMediaQuery'
import { readStoredFlag, writeStoredFlag } from '@/lib/storage'
import { plural, timeAgo } from '@/lib/format'

const DRAWER_STORAGE_KEY = 'accountsDrawerOpen'

export interface AppShellProps {
  onRefreshAll?: () => void
  refreshing?: boolean
  /** Overrides the live feed. Only tests pass this. */
  notifications?: readonly ShellNotification[]
  accounts?: AccountsDrawerProps
}

/** One feed card in the shape the header draws. */
function asShellNotification(card: NotificationCard): ShellNotification {
  return {
    id: card.id,
    title: card.title,
    body: card.body,
    when: timeAgo(card.created_at, 'long'),
    unread: card.read_at === null,
    href: card.url === '' ? undefined : card.url,
  }
}

/**
 * The frame every page hangs in: rail, header, accounts drawer, page. What it
 * displays can arrive as props, so it renders and tests without a server;
 * otherwise it reads its own feed, sync state and account tree, since the
 * routes mount `<AppShell />` bare.
 */
export function AppShell(props: AppShellProps) {
  // Set during render rather than in an effect: the formatters read it as the
  // frame below renders. Keyed on it, so a locale change redraws memoized
  // figures.
  const { user } = useAuth()
  setDisplayLocale(user?.locale)
  const locale = displayLocale() ?? ''

  return (
    <HeaderOverrideProvider>
      {/* Inside the shell: only a signed-in page can ask about a row, and the
          dialog links into the router. */}
      <AskAssistantProvider>
        {/* The dialogs outside the frame, whose key is the locale: a
            provider sign-in is minutes of a server's work, and choosing
            another locale must not throw it away. */}
        <SignInTasksProvider>
          <AppShellFrame key={locale} {...props} />
          <SignInDialogs />
          <FailureScreenshotDialog />
        </SignInTasksProvider>
      </AskAssistantProvider>
    </HeaderOverrideProvider>
  )
}

function AppShellFrame({
  onRefreshAll,
  refreshing,
  notifications,
  accounts,
}: AppShellProps) {
  const { pathname, search } = useLocation()

  // The document is what scrolls (shell.css) and keeps its offset across a
  // route change, so a path change resets it. Only the path: a change of
  // `search` is made on the page already in view.
  useEffect(() => {
    window.scrollTo(0, 0)
  }, [pathname])

  // A fade on arrival, for the top-level section only: a page that swaps a
  // nested route under a sidebar or tab strip is not being replaced. A layout
  // effect, so the first visible frame is the animation's first; not on the
  // shell's own first paint, which is not a navigation.
  const section = pathname.split('/')[1] ?? ''
  const page = useRef<HTMLElement>(null)
  const navigated = useRef(false)
  useBrowserLayoutEffect(() => {
    if (!navigated.current) {
      navigated.current = true
      return
    }
    const motion = getComputedStyle(document.documentElement).getPropertyValue('--motion')
    if (motionIsOff(motion)) return
    restartArrival(page.current)
  }, [section])

  const [storedOpen, setStoredOpen] = useState(() =>
    readStoredFlag(DRAWER_STORAGE_KEY, true),
  )

  // Below this width the drawer floats over the page (shell.css). A floating
  // drawer does not use the remembered open state, and any navigation closes it.
  const floating = useMediaQuery('(max-width: 64rem)')
  // Held as the URL it was opened on, so navigating away closes it with no
  // effect. The search string counts: picking an account changes only
  // `?displayNode=`.
  const [floatOpenAt, setFloatOpenAt] = useState<string | null>(null)
  const here = pathname + search
  const floatOpen = floatOpenAt === here
  const drawerOpen = floating ? floatOpen : storedOpen

  // formatMoney is called from places with no React context (cell formatters,
  // chart ticks), so the always-mounted shell sets the module-level currency.
  const space = useCurrentSpace()
  useEffect(() => {
    if (space.data?.primary_currency) setDisplayCurrency(space.data.primary_currency)
  }, [space.data?.primary_currency])

  // `isSuccess` rather than an empty `data`: a failed listing is not a listing
  // of nothing.
  const spaces = useSpaces()
  const spaceless = spaces.isSuccess && spaces.data.length === 0
  const spaceName = spaces.isSuccess && spaces.data.length > 1 ? space.data?.name : undefined

  const { user } = useAuth()
  const { setActiveSpace } = useActiveSpace()
  const initials = user ? initialsFor(user.full_name, user.email) : null

  // The shell syncs on its own, like its feed and account tree; the prop is an
  // override.
  const connections = useConnections()
  const syncOne = useSyncConnection()
  // The shell is rendered without a provider by its own test, and a missing
  // toast is not worth throwing out of the frame every page hangs in.
  const toast = useToastOrNull()
  const navigate = useNavigate()
  const live = (connections.data?.connections ?? []).filter((one) => !one.needs_setup_token)
  const running = (connections.data?.connections ?? []).filter(isSyncing)
  const syncingAll = syncOne.isPending || running.length > 0
  const syncStatus =
    running.length === 1 && running[0].sync !== null
      ? syncProgressShort(running[0].sync)
      : running.length > 1
        ? `Syncing ${plural(running.length, 'connection')}`
        : undefined
  // While the listing is still in flight there is no answer yet, and a control
  // greyed out with "No connected accounts" would be stating one.
  const canRefresh = connections.isSuccess ? live.length > 0 : true
  // Each runs on the server in the background; how it went is reported as it
  // ends, below, however it was started.
  const refreshAll = () => {
    if (live.length === 0 || syncingAll) return
    for (const one of live) syncOne.mutate(one.id)
  }
  const syncEnded = useCallback(
    (connection: Connection) => {
      toast?.show(syncEndedToast(connection, () => navigate(CONNECT_PATH)))
    },
    [toast, navigate],
  )
  useSyncEnded(syncEnded)

  // The shell reads the feed for the same reason it reads the account tree:
  // nothing above it can supply one, since the routes mount it directly.
  const [unreadOnly, setUnreadOnly] = useState(false)
  const cards = useNotificationFeed(unreadOnly)
  const markRead = useMarkNotificationRead()
  const markAllRead = useMarkAllNotificationsRead()
  const clear = useClearNotification()
  const clearAll = useClearAllNotifications()
  const feed = useMemo(
    () => (cards.data?.notifications ?? []).map(asShellNotification),
    [cards.data],
  )

  // A page in a mode of its own — the phone register while rows are being
  // selected — draws its own bar where the header's row would be.
  const override = useHeaderOverrideNode()

  const drawerAvailable = showsAccountsDrawer(pathname)

  const toggleDrawer = () => {
    if (floating) {
      setFloatOpenAt(floatOpen ? null : here)
      return
    }
    setStoredOpen((open) => {
      writeStoredFlag(DRAWER_STORAGE_KEY, !open)
      return !open
    })
  }

  const [railCollapsed, setRailCollapsed] = useState(railStartsCollapsed)
  const toggleRail = () =>
    setRailCollapsed((current) => {
      storeRailCollapsed(!current)
      return !current
    })

  return (
    <TooltipProvider delayDuration={400}>
      {/* The rail's width is a custom property that the drawer and its backdrop
          are positioned off, so collapsing is one attribute. */}
      <div className="app" data-rail={railCollapsed ? 'collapsed' : undefined}>
        <NavRail
          onRefreshAll={onRefreshAll ?? refreshAll}
          refreshing={refreshing ?? syncingAll}
          refreshStatus={refreshing === undefined ? syncStatus : undefined}
          canRefresh={onRefreshAll ? true : canRefresh}
          collapsed={railCollapsed}
          onToggleCollapsed={toggleRail}
        />

        <div className="app__main">
          <AppHeader
            title={titleForPath(pathname)}
            notifications={notifications ?? feed}
            unreadCount={notifications ? undefined : cards.data?.unread}
            unreadOnly={unreadOnly}
            onUnreadOnlyChange={notifications ? undefined : setUnreadOnly}
            onMarkAllRead={() => markAllRead.mutate()}
            onClearAll={() => clearAll.mutate()}
            onOpenNotification={(id) => markRead.mutate(id)}
            onClearNotification={(id) => clear.mutate(id)}
            initials={initials ?? undefined}
            space={spaceName}
            spaces={spaces.data}
            activeSpaceId={space.data?.id}
            onSwitchSpace={(id) => {
              if (id === space.data?.id) return
              setActiveSpace(id)
              // The page behind names rows of the space being left.
              navigate('/')
            }}
            accounts={drawerAvailable ? { open: drawerOpen, onToggle: toggleDrawer } : undefined}
            override={override}
          />

          {/* Nothing exists outside a space, so a caller with no accepted
              membership gets the way in. */}
          {spaceless ? (
            <div className="app__content">
              <NoSpaceGate />
            </div>
          ) : (
            <div className="app__content">
              {drawerAvailable ? (
                <>
                  {/* Mounted while closed so it can slide; `inert` keeps the
                      closed box out of the tab order and screen readers. */}
                  {accounts ? (
                    <AccountsDrawer {...accounts} open={drawerOpen} />
                  ) : (
                    <ConnectedAccountsDrawer open={drawerOpen} />
                  )}
                  {floating ? (
                    // The page behind a floating drawer is the way to dismiss
                    // it, the same as any sheet; the strip that opened it is
                    // under the drawer's own edge on a phone.
                    <button
                      type="button"
                      className="drawer-backdrop"
                      data-open={String(drawerOpen)}
                      aria-label="Hide accounts"
                      onClick={toggleDrawer}
                      inert={!drawerOpen}
                    />
                  ) : null}
                </>
              ) : null}

              <PullToRefresh />
              <main className="app__page" ref={page}>
                {/* Above the page: an invitation to a second space cannot be
                    reached from a screen reading the first. */}
                <PendingInvitations />
                <NoAccountsNotice />
                <ErrorBoundary scope="page" resetKey={pathname}>
                  <Suspense fallback={<SkeletonRows rows={6} />}>
                    <Outlet />
                  </Suspense>
                </ErrorBoundary>
              </main>
            </div>
          )}
        </div>

        <TabBar
          onRefreshAll={onRefreshAll ?? refreshAll}
          refreshing={refreshing ?? syncingAll}
          refreshStatus={refreshing === undefined ? syncStatus : undefined}
          canRefresh={onRefreshAll ? true : canRefresh}
        />

        <SignInToasts />
      </div>
    </TooltipProvider>
  )
}
