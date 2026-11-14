import { ChartPie, House, Inbox, ListChecks, Wallet, type LucideIcon } from 'lucide-react'

/** One page inside a destination, drawn as a tab across the top of it. */
export interface Section {
  path: string
  label: string
}

export interface Destination {
  path: string
  label: string
  icon: LucideIcon
  /**
   * The pages the destination holds, the first at `path`. A destination with
   * one page has none, and draws no tabs.
   */
  sections: readonly Section[]
}

/**
 * The destinations, in the order the rail and the phone's tab bar show them.
 * Review is the second: checking new money is the main chore, and the rest of
 * the app is where a decision made there is read back.
 */
export const DESTINATIONS: readonly Destination[] = [
  { path: '/', label: 'Home', icon: House, sections: [] },
  { path: '/review', label: 'Review', icon: Inbox, sections: [] },
  {
    path: '/transactions',
    label: 'Activity',
    icon: ListChecks,
    sections: [
      { path: '/transactions', label: 'Transactions' },
      { path: '/rules', label: 'Rules' },
    ],
  },
  {
    path: '/spending-plan',
    label: 'Plan',
    icon: Wallet,
    sections: [
      { path: '/spending-plan', label: 'Spending plan' },
      { path: '/upcoming', label: 'Bills & income' },
      { path: '/goals', label: 'Goals' },
      { path: '/watchlist', label: 'Watchlists' },
      { path: '/planning-tools', label: 'Planning tools' },
    ],
  },
  {
    path: '/reports',
    label: 'Insights',
    icon: ChartPie,
    sections: [
      { path: '/reports', label: 'Reports' },
      { path: '/net-worth', label: 'Net worth' },
      { path: '/investing', label: 'Investments' },
    ],
  },
]

/** Whether `pathname` is `path` or a page beneath it. */
export function under(path: string, pathname: string): boolean {
  if (path === '/') return pathname === '/'
  return pathname === path || pathname.startsWith(`${path}/`)
}

/** The destination the page belongs to, through any of its sections. */
export function destinationFor(pathname: string): Destination | undefined {
  return DESTINATIONS.find(
    (destination) =>
      under(destination.path, pathname) ||
      destination.sections.some((section) => under(section.path, pathname)),
  )
}

/** The section of its destination the page is, if the destination has sections. */
export function sectionFor(pathname: string): Section | undefined {
  return destinationFor(pathname)?.sections.find((section) => under(section.path, pathname))
}

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
  { path: '/settings/duplicates', label: 'Possible duplicates' },
  { path: '/settings/transfers', label: 'Transfer activity' },
] as const

/**
 * The header title: the destination's name, so it does not change under
 * somebody switching between its tabs. Pages outside the destinations name
 * themselves here.
 */
export function titleForPath(pathname: string): string {
  if (pathname.startsWith('/settings')) return 'Settings'
  if (under('/assistant', pathname)) return 'Assistant'
  return destinationFor(pathname)?.label ?? 'Agentifi'
}
