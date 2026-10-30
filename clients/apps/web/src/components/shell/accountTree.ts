import { formatTimestamp, plural } from '@/lib/format'
import { isUuid } from '@/lib/uuid'
import {
  ZERO_MONEY,
  displayCurrency,
  subMoney,
  sumByCurrency,
  type CurrencyAmount,
  type Money,
  type MoneyFormatter,
} from '@/lib/money'
import type { AccountKind, AccountWithBalances, Uuid } from '@/lib/transactions/types'

export type AccountNodeKind =
  /** Banking, Investments, Assets, Liabilities. */
  | 'class'
  /** Cash & Checking, Credit, Savings, Retirement, Home Loan… */
  | 'group'
  | 'account'
  /** Money reserved inside a real account by savings goals. */
  | 'reserved'
  /** What is left of that account once the goals are reserved. */
  | 'available'
  /** What is left of a financed asset once the loans on it are counted. */
  | 'equity'

export interface AccountNode {
  id: string
  name: string
  kind: AccountNodeKind
  /** Set on anything that owns a figure; unset on a class or group, whose totals are derived. */
  balance?: Money
  /** ISO 4217 of `balance`; unset means the display currency. */
  currency?: string
  children?: readonly AccountNode[]
  /** The warning triangle: the connection to this account is broken. */
  connectionError?: boolean
  /** The balance shown is a held last-known value — the feed reported a
   *  suspicious zero the sync declined to apply. */
  held?: HeldBalance
  closed?: boolean
  /** Hidden by the server for a tiny balance: still in the totals and scopes, left out only by the drawer (`shownChildren`). */
  hiddenSmallBalance?: boolean
}

/** What the feed reported and when, for a balance the sync declined to apply. */
export interface HeldBalance {
  reported: Money
  at: string
}

export const CONNECTION_ERROR_TEXT =
  'Connection error: the connection that syncs this account is failing. Settings › Accounts says what it needs.'

/** The held balance's warning, worded from what the feed reported. */
export function heldBalanceText(held: HeldBalance, moneyText: MoneyFormatter): string {
  return (
    `Balance held: SimpleFIN reported ${moneyText(held.reported)} on ` +
    `${formatTimestamp(held.at, 'date')}, which the account's history doesn't explain. ` +
    'The last good balance is shown until you confirm or keep it in Settings › Accounts.'
  )
}

/**
 * The rolled-up total for one node. An own balance wins over the children: a
 * savings account's goal and available children partition its balance, and
 * summing them would count the reserve twice.
 */
export function nodeTotals(node: AccountNode): CurrencyAmount[] {
  if (node.balance !== undefined) {
    return [{ currency: node.currency ?? displayCurrency(), amount: node.balance }]
  }
  return treeTotals(node.children ?? [])
}

/** One total per currency: balances in different currencies are never added. */
export function treeTotals(nodes: readonly AccountNode[]): CurrencyAmount[] {
  return sumByCurrency(nodes.flatMap(nodeTotals))
}

/** The single total of a tree whose figures are all in one currency (net worth's, converted by the server). */
export function nodeTotal(node: AccountNode): Money {
  return single(nodeTotals(node))
}

export function treeTotal(nodes: readonly AccountNode[]): Money {
  return single(treeTotals(nodes))
}

function single(totals: readonly CurrencyAmount[]): Money {
  if (totals.length > 1) throw new TypeError('a mixed-currency tree has no single total')
  return totals[0]?.amount ?? ZERO_MONEY
}

/**
 * The rows a list draws, and how many small balances it left out. Display
 * only: whatever the list totals still sums every row.
 */
export function withoutSmallBalances<T>(
  rows: readonly T[],
  isSmall: (row: T) => boolean,
  revealed: boolean,
): { shown: readonly T[]; hidden: number } {
  const hidden = rows.filter(isSmall).length
  if (revealed || hidden === 0) return { shown: rows, hidden }
  return { shown: rows.filter((row) => !isSmall(row)), hidden }
}

/** The children a drawer row draws, and how many small balances it left out. */
export function shownChildren(
  node: AccountNode,
  revealed: boolean,
): { shown: readonly AccountNode[]; hidden: number } {
  return withoutSmallBalances(
    node.children ?? [],
    (child) => child.hiddenSmallBalance === true,
    revealed,
  )
}

/** "3 small balances hidden", the line that stands in for the rows it hid, or "Hide 3 small balances" once revealed. */
export function smallBalancesLabel(count: number, revealed: boolean): string {
  const balances = plural(count, 'small balance')
  return revealed ? `Hide ${balances}` : `${balances} hidden`
}

/** Flattened ids of a node and everything under it, for expand-all state. */
export function collectIds(nodes: readonly AccountNode[]): string[] {
  return nodes.flatMap((node) => [node.id, ...collectIds(node.children ?? [])])
}

/** The root row, which is every account rather than a group of them. */
export const ALL_ACCOUNTS_ID = 'all'

/**
 * The two classes asked about by name: Banking is where the register opens
 * when nothing is picked (`lib/accountScope`); Liabilities is the dashboard's.
 */
export const BANKING_CLASS_ID = 'class:banking'
export const LIABILITIES_CLASS_ID = 'class:liabilities'
const ASSETS_CLASS_ID = 'class:assets'

/**
 * The group structure the drawer renders. The ids are deliberately not uuids:
 * `displayNode` carries one, and the register tells an account from a group by
 * whether it parses as a uuid.
 */
const ACCOUNT_TREE_SHAPE = [
  {
    id: BANKING_CLASS_ID,
    name: 'Banking',
    groups: [
      { id: 'group:cash', name: 'Cash & Checking' },
      { id: 'group:credit', name: 'Credit' },
      { id: 'group:savings', name: 'Savings' },
    ],
  },
  {
    id: 'class:investments',
    name: 'Investments',
    groups: [
      { id: 'group:retirement', name: 'Retirement' },
      { id: 'group:crypto', name: 'Crypto' },
      { id: 'group:life-insurance', name: 'Life Insurance' },
      { id: 'group:other-investments', name: 'Other Investments' },
    ],
  },
  {
    id: ASSETS_CLASS_ID,
    name: 'Assets',
    groups: [
      { id: 'group:real-estate', name: 'Real Estate' },
      { id: 'group:vehicle', name: 'Vehicle' },
      { id: 'group:other-assets', name: 'Other Assets' },
    ],
  },
  {
    id: LIABILITIES_CLASS_ID,
    name: 'Liabilities',
    groups: [
      { id: 'group:home-loan', name: 'Home Loan' },
      { id: 'group:vehicle-loan', name: 'Vehicle Loan' },
      { id: 'group:personal-loan', name: 'Personal Loan' },
    ],
  },
] as const

/** Cash accounts that belong under Savings rather than Cash & Checking. */
const SAVINGS_TYPES = new Set(['savings', 'money_market', 'cd'])

/** Investment accounts that belong under Retirement. */
const RETIREMENT_TYPES = new Set([
  '401k',
  'roth_401k',
  '403b',
  'ira',
  'roth_ira',
  'sep_ira',
  'simple_ira',
  'keogh',
])

/** Loans secured on a home, whatever the picker called them. */
const HOME_LOAN_TYPES = new Set(['mortgage', 'home_equity_loan', 'construction_loan'])

/**
 * Which drawer group one account belongs in: `kind` settles the class, `type`
 * the group inside it. An unknown type lands in its class's *other* group,
 * since an account missing from the tree is missing from its total.
 */
export function groupIdFor(account: { kind: AccountKind; type: string }): string {
  switch (account.kind) {
    case 'cash':
      return SAVINGS_TYPES.has(account.type) ? 'group:savings' : 'group:cash'
    case 'credit_card':
      return 'group:credit'
    case 'investment':
      if (RETIREMENT_TYPES.has(account.type)) return 'group:retirement'
      if (account.type === 'crypto') return 'group:crypto'
      if (account.type === 'life_insurance') return 'group:life-insurance'
      return 'group:other-investments'
    case 'loan':
      if (HOME_LOAN_TYPES.has(account.type)) return 'group:home-loan'
      if (account.type === 'vehicle_loan') return 'group:vehicle-loan'
      return 'group:personal-loan'
    case 'asset':
      if (account.type === 'real_estate') return 'group:real-estate'
      if (account.type === 'vehicle') return 'group:vehicle'
      return 'group:other-assets'
    default:
      return 'group:other-assets'
  }
}

const CLASS_BY_GROUP = new Map<string, string>(
  ACCOUNT_TREE_SHAPE.flatMap((klass) => klass.groups.map((group) => [group.id, klass.id])),
)

/**
 * Which drawer class one account rolls up into, read off the shape rather than
 * switching on `kind` again. The fallback is unreachable; a Map lookup is
 * typed as possibly absent.
 */
export function classIdFor(account: { kind: AccountKind; type: string }): string {
  return CLASS_BY_GROUP.get(groupIdFor(account)) ?? ASSETS_CLASS_ID
}

/**
 * `GET /accounts` as the drawer's tree. Closed accounts are left out, as the
 * register's "All Accounts" total leaves them out. Empty groups and classes
 * are dropped rather than drawn at zero.
 */
export function buildAccountTree(
  accounts: readonly AccountWithBalances[],
  types?: readonly string[] | null,
): AccountNode[] {
  // `null` and `undefined` list every type; an empty array is a user who
  // unticked them all.
  const wanted = types == null ? null : new Set(types)

  const byGroup = new Map<string, AccountNode[]>()
  for (const account of [...accounts].sort(inDrawerOrder)) {
    if (account.is_closed) continue
    if (wanted && !wanted.has(account.type)) continue
    const id = groupIdFor(account)
    const members = byGroup.get(id) ?? []
    members.push(accountNode(account))
    byGroup.set(id, members)
  }

  const tree: AccountNode[] = []
  for (const klass of ACCOUNT_TREE_SHAPE) {
    const groups: AccountNode[] = []
    for (const group of klass.groups) {
      const members = byGroup.get(group.id)
      if (members === undefined) continue
      groups.push({ id: group.id, name: group.name, kind: 'group', children: members })
    }
    if (groups.length > 0) {
      tree.push({ id: klass.id, name: klass.name, kind: 'class', children: groups })
    }
  }
  return tree
}

function inDrawerOrder(a: AccountWithBalances, b: AccountWithBalances): number {
  if (a.sort_order !== b.sort_order) return a.sort_order - b.sort_order
  return a.name.localeCompare(b.name)
}

/**
 * One account, split by its savings goals and annotated with its equity when
 * financed. Every child is an annotation: `nodeTotal` ignores an account's
 * children, so none reach the group total.
 */
function accountNode(account: AccountWithBalances): AccountNode {
  const balance = account.balances.balance
  const currency = account.currency
  const node: AccountNode = {
    id: account.id,
    name: account.name,
    kind: 'account',
    balance,
    currency,
  }
  if (account.withheld_balance_at) {
    node.held = { reported: account.withheld_balance ?? ZERO_MONEY, at: account.withheld_balance_at }
  }
  if (account.hidden_small_balance) {
    node.hiddenSmallBalance = true
  }
  if (account.goal_balance !== ZERO_MONEY) {
    node.children = [
      {
        id: `${account.id}:goals`,
        name: 'Savings Goals',
        kind: 'reserved',
        balance: account.goal_balance,
        currency,
      },
      {
        id: `${account.id}:available`,
        name: 'Available Balance',
        kind: 'available',
        balance: subMoney(balance, account.goal_balance),
        currency,
      },
    ]
  }
  if (account.equity) {
    node.children = [
      ...(node.children ?? []),
      {
        id: `${account.id}:equity`,
        name: 'Equity',
        kind: 'equity',
        balance: account.equity.equity,
        currency,
      },
    ]
  }
  return node
}

/** `id` is a uuid, so it names an account rather than a group of them. */
function isAccountId(id: string): boolean {
  return isUuid(id)
}

/**
 * The accounts one drawer node scopes the register to. `null` is every
 * account and the empty array is none, kept apart to the wire: a group whose
 * accounts are all closed selects nothing, not the whole ledger. A uuid
 * resolves without the tree; an unknown group id selects nothing.
 */
export function accountIdsFor(
  tree: readonly AccountNode[],
  nodeId: string | null,
): readonly Uuid[] | null {
  if (nodeId === null || nodeId === ALL_ACCOUNTS_ID) return null
  if (isAccountId(nodeId)) return [nodeId]
  const node = findNode(tree, nodeId)
  return node === null ? [] : accountIdsUnder(node)
}

function findNode(nodes: readonly AccountNode[], id: string): AccountNode | null {
  for (const node of nodes) {
    if (node.id === id) return node
    const found = findNode(node.children ?? [], id)
    if (found !== null) return found
  }
  return null
}

function accountIdsUnder(node: AccountNode): Uuid[] {
  if (node.kind === 'account') return [node.id]
  return (node.children ?? []).flatMap(accountIdsUnder)
}

/** The label a node carries, for a header that has to name what it is showing. */
export function nodeName(tree: readonly AccountNode[], nodeId: string | null): string | null {
  if (nodeId === null || nodeId === ALL_ACCOUNTS_ID) return null
  return findNode(tree, nodeId)?.name ?? null
}
