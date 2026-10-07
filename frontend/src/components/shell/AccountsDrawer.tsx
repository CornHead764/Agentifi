import { ChevronDown, ChevronRight, Plus } from 'lucide-react'
import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

import { MoneyTotals } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { CONNECT_PATH, IMPORT_PATH } from '@/components/onboarding/paths'
import { Button, Callout, EmptyState, SkeletonRows, WarningMark } from '@/components/ui'
import type { CurrencyAmount } from '@/lib/money'

import { defaultScopeNode, useChosenScopeNode } from '@/lib/accountScope'
import { depthStyle } from '@/lib/depthStyle'
import { toggledSet } from '@/lib/toggle'

import {
  ALL_ACCOUNTS_ID,
  CONNECTION_ERROR_TEXT,
  heldBalanceText,
  nodeTotals,
  shownChildren,
  smallBalancesLabel,
  treeTotals,
  type AccountNode,
} from './accountTree'
import { groupCollapsed, toggleGroupCollapsed } from './groupCollapse'

export interface AccountsDrawerProps {
  /** The four classes. Undefined means "not loaded yet", empty means "none". */
  tree?: readonly AccountNode[]
  /** Why the tree is missing, when it is missing because the read failed. */
  error?: unknown
  onNewAccount?: () => void
  /**
   * Whether the drawer is showing. Closed it is still rendered, so it can
   * slide; `shell.css` sets the width.
   */
  open?: boolean
}

/**
 * The accounts drawer: a rolled-up balance tree. Group totals are derived from
 * the leaves, so a group cannot disagree with the accounts under it.
 */
export function AccountsDrawer({
  tree,
  error,
  onNewAccount,
  open = true,
}: AccountsDrawerProps) {
  const [collapsed, setCollapsed] = useState<ReadonlyMap<string, boolean>>(new Map())
  const [revealed, setRevealed] = useState<ReadonlySet<string>>(new Set())
  const [params] = useSearchParams()
  // Resolved as the register resolves it: the node this page names, else the
  // last one picked, else the default.
  const remembered = useChosenScopeNode()
  const chosen = params.get('displayNode') ?? remembered
  const selected = chosen ?? (tree === undefined ? null : defaultScopeNode(tree))

  const toggle = (id: string) => setCollapsed((current) => toggleGroupCollapsed(current, id))
  const reveal = (id: string) => setRevealed((current) => toggledSet(current, id))

  return (
    // `inert` while it is closed: a box of no width still holds every account
    // link, and a tab that lands in one is a cursor nobody can see.
    <div className="drawer" data-open={String(open)} inert={!open}>
      <div className="drawer__header">
        <h2 className="drawer__title">Accounts</h2>
        <Button variant="ghost" size="sm" onClick={onNewAccount}>
          <Plus size={14} />
          New
        </Button>
      </div>

      <div className="drawer__tree">
        {tree === undefined ? (
          // A failed read keeps its skeleton forever otherwise, which reads as
          // an account list that is still coming.
          error ? (
            <Callout tone="expense" className="drawer__empty">The account list did not load.</Callout>
          ) : (
            <SkeletonRows rows={8} className="drawer__loading" />
          )
        ) : (
          <>
            <AccountRow
              node={{
                id: ALL_ACCOUNTS_ID,
                name: 'All Accounts',
                kind: 'class',
              }}
              totals={treeTotals(tree)}
              depth={0}
              selected={selected}
              collapsed={collapsed}
              onToggle={toggle}
              revealed={revealed}
              onReveal={reveal}
            />
            {tree.map((node) => (
              <AccountRow
                key={node.id}
                node={node}
                depth={0}
                selected={selected}
                collapsed={collapsed}
                onToggle={toggle}
                revealed={revealed}
                onReveal={reveal}
              />
            ))}
            {tree.length === 0 ? (
              <EmptyState
                compact
                className="drawer__empty"
                title={
                  <>
                    No accounts yet. <Link to={CONNECT_PATH}>Connect your banks</Link>,{' '}
                    <Link to={IMPORT_PATH}>import a statement</Link>, or add one by hand with New.
                  </>
                }
              />
            ) : null}
          </>
        )}
      </div>
    </div>
  )
}

interface AccountRowProps {
  node: AccountNode
  /** The row's figure when it is not the node's own roll-up. */
  totals?: readonly CurrencyAmount[]
  depth: number
  selected: string | null
  /** Rows toggled this session; the rest are as stored. */
  collapsed: ReadonlyMap<string, boolean>
  onToggle: (id: string) => void
  /** Groups whose small balances are drawn anyway. */
  revealed: ReadonlySet<string>
  onReveal: (id: string) => void
}

function AccountRow({
  node,
  totals,
  depth,
  selected,
  collapsed,
  onToggle,
  revealed,
  onReveal,
}: AccountRowProps) {
  const children = node.children ?? []
  const isRevealed = revealed.has(node.id)
  const { shown, hidden } = shownChildren(node, isRevealed)
  const expandable = children.length > 0
  const isOpen = expandable && !groupCollapsed(collapsed, node.id)
  const moneyText = useMoneyText()
  const isActive =
    selected === node.id || (selected === null && node.id === ALL_ACCOUNTS_ID)

  // Reserved, available and equity are readouts of the account above, not
  // scopes for the register.
  const navigable =
    node.kind !== 'reserved' && node.kind !== 'available' && node.kind !== 'equity'

  const contents = (
    <>
      <span className="acct-row__name" title={node.name}>
        {node.name}
      </span>
      {node.connectionError ? (
        <WarningMark text={CONNECTION_ERROR_TEXT} side="right" focusable={false} />
      ) : null}
      {node.held ? (
        <WarningMark
          text={heldBalanceText(node.held, moneyText)}
          side="right"
          focusable={false}
        />
      ) : null}
      {/* Balances are neutral. A negative here is a debt, not money going out,
          and the two must not share a colour. */}
      <MoneyTotals
        totals={totals ?? nodeTotals(node)}
        tone="neutral"
        className="acct-row__amount"
      />
    </>
  )

  const className = [
    'acct-row',
    `acct-row--${node.kind}`,
    node.closed ? 'acct-row--closed' : null,
    isActive ? 'acct-row--active' : null,
  ]
    .filter(Boolean)
    .join(' ')

  return (
    <>
      <div className={className} style={depthStyle(depth)}>
        {expandable ? (
          <button
            type="button"
            className="acct-row__twisty"
            aria-expanded={isOpen}
            aria-label={`${isOpen ? 'Collapse' : 'Expand'} ${node.name}`}
            onClick={() => onToggle(node.id)}
          >
            {isOpen ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          </button>
        ) : (
          <span className="acct-row__twisty" />
        )}

        {navigable ? (
          <Link
            className="acct-row__link"
            to={`/transactions?displayNode=${encodeURIComponent(node.id)}`}
            aria-current={isActive ? 'page' : undefined}
          >
            {contents}
          </Link>
        ) : (
          <span className="acct-row__link">{contents}</span>
        )}
      </div>

      {isOpen
        ? shown.map((child) => (
            <AccountRow
              key={child.id}
              node={child}
              depth={depth + 1}
              selected={selected}
              collapsed={collapsed}
              onToggle={onToggle}
              revealed={revealed}
              onReveal={onReveal}
            />
          ))
        : null}

      {/* Said out loud, so an account that holds too little to list is one
          click away rather than silently gone. */}
      {isOpen && hidden > 0 ? (
        <div
          className="acct-row acct-row--small-balances"
          style={depthStyle(depth + 1)}
        >
          <span className="acct-row__twisty" />
          <button
            type="button"
            className="acct-row__reveal"
            aria-expanded={isRevealed}
            onClick={() => onReveal(node.id)}
          >
            {smallBalancesLabel(hidden, isRevealed)}
          </button>
        </div>
      ) : null}
    </>
  )
}
