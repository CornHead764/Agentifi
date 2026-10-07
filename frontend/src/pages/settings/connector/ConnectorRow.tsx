import type { ReactNode } from 'react'

import { Callout, type OverflowAction, OverflowMenu, RowActions } from '@/components/ui'

import { engineSentence, type EngineState } from './engine'

/**
 * One login at a bill provider, a shop or a mailbox, drawn the same way on
 * every settings page. One visible action (sign in when missing, otherwise
 * update); the rest waits in the menu. `note` needs reading now; `details` is
 * folded.
 */
export function ConnectorRow({
  title,
  badge,
  meta = [],
  primary,
  aside,
  actions = [],
  actionsLabel,
  note,
  details,
  dimmed = false,
  children,
}: {
  title: ReactNode
  badge?: ReactNode
  /** Short facts, joined by dots the stylesheet draws; empty ones are dropped. */
  meta?: readonly ReactNode[]
  primary?: ReactNode
  /** A standing setting drawn before the primary action — a Daily switch. */
  aside?: ReactNode
  actions?: readonly OverflowAction[]
  /** The menu button's accessible name: "Actions for Example Utility · Main account". */
  actionsLabel: string
  note?: ReactNode
  details?: ReactNode
  dimmed?: boolean
  children?: ReactNode
}) {
  const facts = meta.filter(
    (one) => one !== null && one !== undefined && one !== '' && one !== false,
  )
  const controls = primary || aside
  return (
    <li className={dimmed ? 'connector connector--dimmed' : 'connector'}>
      <div className={primary ? 'connector__head' : 'connector__head connector__head--inline'}>
        <div className="connector__who">
          <span className="connector__title">
            <strong>{title}</strong>
            {badge}
          </span>
          {facts.length > 0 ? (
            <span className="connector__meta">
              {facts.map((fact, index) => (
                <span key={index}>{fact}</span>
              ))}
            </span>
          ) : null}
        </div>
        {controls ? (
          <div className="connector__controls">
            {aside}
            {primary}
          </div>
        ) : null}
        {actions.length > 0 ? (
          <RowActions>
            <OverflowMenu label={actionsLabel} actions={actions} />
          </RowActions>
        ) : null}
      </div>
      {note}
      {details ? (
        <details className="connector__details hint">
          <summary>Details</summary>
          <div className="connector__details-body">{details}</div>
        </details>
      ) : null}
      {children}
    </li>
  )
}

/** A line inside a row's folded details, with its icon. */
export function ConnectorFact({ icon, children }: { icon?: ReactNode; children: ReactNode }) {
  return (
    <p className="connector__fact">
      {icon ? <span className="connector__fact-icon">{icon}</span> : null}
      <span>{children}</span>
    </p>
  )
}

/** The rows of one page, as a list the row layout measures itself against. */
export function ConnectorList({ children }: { children: ReactNode }) {
  return <ul className="connectors">{children}</ul>
}

/** Why nothing on the page can sign in, said once above the rows; `after` says what still works. */
export function EngineNotice({ engine, after }: { engine: EngineState; after?: string }) {
  const sentence = engineSentence(engine)
  if (sentence === null) return null
  return (
    <Callout tone="warning" role="status">
      {after ? `${sentence} ${after}` : sentence}
    </Callout>
  )
}
