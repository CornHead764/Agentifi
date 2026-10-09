import type { Page } from '@playwright/test'

import { ACCOUNT, CATEGORY, id } from './fixtures/core'

export interface LayoutRoute {
  /** What a violation and the allowlist call this route. */
  name: string
  path: string
  /** Clicks after the first load, for a tab that is state rather than a URL. */
  open?: (page: Page) => Promise<void>
}

export interface LayoutWidth {
  width: number
  height: number
  /** Opens the accounts drawer on the pages that carry it. */
  drawerOpen?: boolean
}

export const WIDTHS: readonly LayoutWidth[] = [
  { width: 320, height: 640 },
  { width: 375, height: 667 },
  { width: 390, height: 844 },
  { width: 414, height: 896 },
  { width: 768, height: 1024 },
  { width: 820, height: 1180 },
  { width: 1180, height: 820, drawerOpen: true },
  { width: 1440, height: 900 },
]

function tab(name: string, list?: string) {
  return async (page: Page) => {
    const scope = list ? page.getByRole('tablist', { name: list }) : page
    await scope.getByRole('tab', { name, exact: true }).click()
  }
}

function chip(name: string, group: string) {
  return async (page: Page) => {
    await page.getByRole('radiogroup', { name: group }).getByRole('radio', { name, exact: true }).click()
  }
}

/**
 * Every destination in `components/shell/destinations.ts`, every Settings
 * section, and the tabs under them. `layout.spec.ts` fails when a destination
 * or a Settings section is missing here.
 */
export const ROUTES: readonly LayoutRoute[] = [
  { name: 'dashboard', path: '/' },
  { name: 'transactions', path: '/transactions' },
  { name: 'transactions/unreviewed', path: '/transactions?isReviewed=0' },
  {
    name: 'transactions/missing-receipts',
    path: '/transactions?displayNode=all&missingReceipt=1&datePreset=all-time',
  },
  {
    name: 'transactions/owed-receipt',
    path: `/transactions?edit=${id('txn', 1)}`,
    open: async (page) => {
      await page.getByRole('dialog').waitFor()
    },
  },
  { name: 'account', path: `/transactions?displayNode=${ACCOUNT.checking}` },
  { name: 'account/credit-card', path: `/transactions?displayNode=${ACCOUNT.card}` },
  { name: 'account/long-name', path: `/transactions?displayNode=${ACCOUNT.mortgage}` },
  { name: 'account/spending', path: `/transactions?displayNode=${ACCOUNT.checking}&tab=spending` },
  {
    name: 'account/spending/drilled',
    path: `/transactions?displayNode=${ACCOUNT.checking}&tab=spending&drill=category:${CATEGORY.food}&drill=payee:Corner%20Grocery`,
  },
  { name: 'net-worth', path: '/net-worth' },
  { name: 'net-worth/debt', path: '/net-worth', open: tab('Debt') },
  { name: 'spending-plan', path: '/spending-plan' },
  {
    name: 'spending-plan/other-spend',
    path: '/spending-plan',
    open: async (page) => {
      await page.getByText('Other Spend', { exact: true }).first().click()
    },
  },
  { name: 'goals', path: '/goals' },
  { name: 'goals/by-account', path: '/goals', open: tab('By account') },
  {
    name: 'goals/closed',
    path: '/goals',
    open: async (page) => {
      await page.getByRole('button', { name: /^Closed/ }).click()
    },
  },
  {
    name: 'goals/detail',
    path: '/goals',
    open: async (page) => {
      await page.getByRole('button', { name: '4 transactions counted' }).click()
    },
  },
  {
    name: 'goals/find-rows',
    path: '/goals',
    open: async (page) => {
      await page.getByRole('button', { name: 'Find rows' }).first().click()
    },
  },
  { name: 'upcoming', path: '/upcoming' },
  { name: 'upcoming/cash-flow', path: '/upcoming/cash-flow' },
  { name: 'upcoming/recurring', path: '/upcoming/recurring' },
  { name: 'upcoming/recurring/suggested', path: '/upcoming/recurring', open: chip('Suggested', 'Recurring filters') },
  { name: 'upcoming/refund-tracker', path: '/upcoming/refund-tracker' },
  { name: 'planning-tools', path: '/planning-tools' },
  { name: 'investing', path: '/investing' },
  { name: 'investing/balances', path: '/investing', open: tab('Balances') },
  { name: 'investing/performance', path: '/investing', open: tab('Performance') },
  { name: 'investing/transactions', path: '/investing', open: tab('Transactions') },
  { name: 'watchlist', path: '/watchlist' },
  { name: 'reports', path: '/reports' },
  {
    name: 'reports/open',
    path: '/reports',
    open: async (page) => {
      await page.locator('.gallery__name', { hasText: /^Spending$/ }).click()
    },
  },
  ...['Donut', 'Flow', 'Table', 'Transactions'].map((view) => ({
    name: `reports/spending/${view.toLowerCase()}`,
    path: '/reports',
    open: async (page: Page) => {
      await page.locator('.gallery__name', { hasText: /^Spending$/ }).click()
      await chip(view, 'Show the breakdown as')(page)
    },
  })),
  {
    name: 'reports/spending/net-credit',
    path: '/reports',
    open: async (page) => {
      await page.locator('.gallery__name', { hasText: /^Spending$/ }).click()
      for (let step = 0; step < 4; step += 1) {
        await page.getByRole('button', { name: 'Previous period' }).click()
      }
    },
  },
  {
    name: 'reports/spending/year',
    path: '/reports',
    open: async (page) => {
      await page.locator('.gallery__name', { hasText: /^Spending$/ }).click()
      await chip('Year', 'View spend by')(page)
    },
  },
  {
    name: 'reports/income',
    path: '/reports',
    open: async (page) => {
      await page.locator('.gallery__name', { hasText: /^Income$/ }).click()
    },
  },
  { name: 'rules', path: '/rules' },
  { name: 'rules/guidance', path: '/rules/guidance' },
  { name: 'assistant', path: '/assistant' },
  { name: 'assistant/automations', path: '/assistant', open: tab('Automations') },
  { name: 'settings/general', path: '/settings/general' },
  { name: 'settings/accounts', path: '/settings/accounts' },
  {
    name: 'settings/accounts/ignored',
    path: '/settings/accounts',
    open: async (page) => {
      await page.getByRole('button', { name: 'Show ignored accounts' }).click()
      await page.getByRole('button', { name: /^Example Bank/ }).click()
    },
  },
  {
    name: 'settings/accounts/match',
    path: '/settings/accounts',
    open: async (page) => {
      await page.getByRole('button', { name: 'Actions for SimpleFIN' }).click()
      await page.getByRole('menuitem', { name: 'Match accounts…' }).click()
    },
  },
  { name: 'settings/bills', path: '/settings/bills' },
  {
    name: 'settings/bills/edit-autopay-on',
    path: '/settings/bills',
    open: async (page) => {
      await page
        .getByRole('button', { name: 'Actions for Riverside Municipal Water And Sewer Utility District' })
        .click()
      await page.getByRole('menuitem', { name: 'Edit' }).click()
      await page.getByRole('dialog').waitFor()
    },
  },
  {
    name: 'settings/bills/edit-autopay-off',
    path: '/settings/bills',
    open: async (page) => {
      await page.getByRole('button', { name: 'Actions for Example Mobile' }).click()
      await page.getByRole('menuitem', { name: 'Edit' }).click()
      await page.getByRole('dialog').waitFor()
    },
  },
  { name: 'settings/categories-tags', path: '/settings/categories-tags' },
  { name: 'settings/email', path: '/settings/email' },
  { name: 'settings/merchants', path: '/settings/merchants' },
  { name: 'settings/notifications', path: '/settings/notifications' },
  { name: 'settings/security', path: '/settings/security' },
  { name: 'settings/admin', path: '/settings/admin' },
  {
    name: 'settings/admin/new-backup-key',
    path: '/settings/admin',
    open: async (page) => {
      await page.getByRole('button', { name: 'Generate a key pair' }).click()
      await page.getByRole('dialog').waitFor()
    },
  },
  {
    name: 'settings/admin/restore',
    path: '/settings/admin',
    open: async (page) => {
      await page.getByRole('button', { name: 'Actions for 2026-03-03_033000_nightly' }).click()
      await page.getByRole('menuitem', { name: 'Restore…' }).click()
      await page.getByRole('dialog').waitFor()
    },
  },
  { name: 'settings/spaces', path: '/settings/spaces' },
  { name: 'settings/duplicates', path: '/settings/duplicates' },
  { name: 'settings/transfers', path: '/settings/transfers' },
]
