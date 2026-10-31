/**
 * Which register tab is showing: the URL's `tab` when it names one, else the
 * tab the account opens on (`default_register_tab`), else the rows. The URL
 * spells a tab out only when it differs from where the page would open, so a
 * link without one follows the account's setting.
 */

import type { Account, RegisterTab } from './types'

export const REGISTER_TABS: readonly { id: RegisterTab; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'spending', label: 'Spending' },
  { id: 'income', label: 'Income' },
]

/** The tab a register over `account` opens on; the rows for every account at once. */
export function openingTab(account: Pick<Account, 'default_register_tab'> | null): RegisterTab {
  return account?.default_register_tab ?? 'all'
}

export function asRegisterTab(value: string | null): RegisterTab | null {
  return value === 'all' || value === 'spending' || value === 'income' ? value : null
}

export function readTab(params: URLSearchParams, opening: RegisterTab): RegisterTab {
  return asRegisterTab(params.get('tab')) ?? opening
}

/** The URL showing `tab`, every other parameter kept. */
export function withTab(
  params: URLSearchParams,
  tab: string,
  opening: RegisterTab,
): URLSearchParams {
  const next = new URLSearchParams(params)
  const chosen = asRegisterTab(tab) ?? 'all'
  if (chosen === opening) next.delete('tab')
  else next.set('tab', chosen)
  return next
}

export function registerTabLabel(tab: RegisterTab): string {
  return REGISTER_TABS.find((one) => one.id === tab)?.label ?? 'All'
}
