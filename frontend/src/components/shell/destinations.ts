import {
  Bot,
  CalendarDays,
  ChartColumnBig,
  ChartNoAxesCombined,
  ChartPie,
  CircleDollarSign,
  Compass,
  Eye,
  LayoutGrid,
  Target,
  Wallet,
  Wand2,
  type LucideIcon,
} from 'lucide-react'

export interface Destination {
  path: string
  label: string
  /** What the phone's tab bar calls it; everywhere else uses `label`. */
  short?: string
  icon: LucideIcon
  /**
   * The rail draws a hairline between groups: where you are (dashboard,
   * register, net worth), what you plan (spending plan, goals, bills,
   * planning), and what you review (investments, watchlists, reports).
   */
  group: 1 | 2 | 3
}

/** The destinations, in the order the rail shows them. */
export const DESTINATIONS: readonly Destination[] = [
  { path: '/', label: 'Dashboard', short: 'Home', icon: LayoutGrid, group: 1 },
  { path: '/transactions', label: 'Transactions', icon: CircleDollarSign, group: 1 },
  { path: '/net-worth', label: 'Net Worth', icon: ChartNoAxesCombined, group: 1 },
  { path: '/spending-plan', label: 'Spending Plan', short: 'Plan', icon: Wallet, group: 2 },
  { path: '/goals', label: 'Savings Goals', icon: Target, group: 2 },
  { path: '/upcoming', label: 'Bills & Income', short: 'Bills', icon: CalendarDays, group: 2 },
  { path: '/planning-tools', label: 'Planning Tools', icon: Compass, group: 2 },
  { path: '/investing', label: 'Investments', icon: ChartColumnBig, group: 3 },
  { path: '/watchlist', label: 'Watchlists', icon: Eye, group: 3 },
  { path: '/reports', label: 'Reports', icon: ChartPie, group: 3 },
  { path: '/rules', label: 'Rules', icon: Wand2, group: 3 },
  { path: '/assistant', label: 'Assistant', icon: Bot, group: 3 },
]

/**
 * The two pages that carry the accounts drawer. Net worth has its own accounts
 * panel, and two account lists on one screen disagree.
 */
export function showsAccountsDrawer(pathname: string): boolean {
  return pathname === '/' || pathname === '/transactions' || pathname.startsWith('/transactions/')
}

/**
 * `superuser` marks a section only the server's administrator may open. It
 * hides a link and nothing more: the API refuses /admin routes to an ordinary
 * account, and the page refuses to render for one.
 */
export const SETTINGS_SECTIONS = [
  { path: '/settings/general', label: 'General' },
  { path: '/settings/accounts', label: 'Accounts' },
  { path: '/settings/bills', label: 'Bill providers' },
  { path: '/settings/categories-tags', label: 'Categories & tags' },
  { path: '/settings/email', label: 'Email' },
  { path: '/settings/merchants', label: 'Merchants' },
  { path: '/settings/notifications', label: 'Notifications' },
  { path: '/settings/security', label: 'Security' },
  { path: '/settings/admin', label: 'Server admin', superuser: true },
  { path: '/settings/spaces', label: 'Spaces & sharing' },
  { path: '/settings/transfers', label: 'Transfer activity' },
] as const

/** The header title. Longest match wins, so `/spending-plan` is not `/`. */
export function titleForPath(pathname: string): string {
  if (pathname.startsWith('/settings')) return 'Settings'

  let best: Destination | undefined
  for (const destination of DESTINATIONS) {
    const matches =
      destination.path === '/'
        ? pathname === '/'
        : pathname === destination.path || pathname.startsWith(`${destination.path}/`)
    if (matches && (!best || destination.path.length > best.path.length)) best = destination
  }
  return best?.label ?? 'Agentifi'
}
