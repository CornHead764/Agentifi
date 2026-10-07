import { Link } from 'react-router-dom'

import { Button, Callout } from '@/components/ui'
import type { ImportWarningGroup } from '@/lib/clients/imports'
import { formatCount } from '@/lib/format'

import { CONNECT_PATH } from './paths'

/** Where each kind of warning is resolved, by the action the server names. */
function actionLink(action: string): { label: string; to: string } | null {
  switch (action) {
    case 'link_accounts':
      return { label: 'Match accounts', to: CONNECT_PATH }
    case 'review_accounts':
      return { label: 'Open accounts', to: '/settings/accounts' }
    case 'review_rules':
      return { label: 'Open rules', to: '/rules' }
    case 'review_categories':
      return { label: 'Open categories', to: '/settings/categories-tags' }
    case 'review_transfers':
      return { label: 'Open transfers', to: '/settings/transfers' }
    case 'review_recurring':
      return { label: 'Open recurring', to: '/upcoming/recurring' }
    case 'review_goals':
      return { label: 'Open goals', to: '/goals' }
    default:
      return null
  }
}

/**
 * What an import found: the errors that stop it, then the warnings one line
 * per kind with the records beneath, then what was not read. Every line the
 * server sent is kept, folded under its kind. Links to where a kind is
 * resolved appear only once there is something there to resolve.
 */
export function ImportProblems({
  errors,
  warnings,
  blocked,
  written,
  onNavigate,
}: {
  errors: string[]
  warnings: ImportWarningGroup[]
  /** The title over the errors: what does not happen until they are fixed. */
  blocked: string
  /** The import has been written, so its warnings can be acted on. */
  written: boolean
  onNavigate?: () => void
}) {
  const losses = warnings.filter((group) => !group.note)
  const notes = warnings.filter((group) => group.note)
  const resolvable = losses.some((group) => actionLink(group.action) !== null)

  return (
    <>
      {errors.length > 0 ? (
        <Callout tone="expense" title={blocked}>
          <ul className="import__problems">
            {errors.map((problem) => (
              <li key={problem}>{problem}</li>
            ))}
          </ul>
        </Callout>
      ) : null}

      {losses.length > 0 ? (
        <Callout tone="warning" title="Warnings">
          <ul className="import-warnings">
            {losses.map((group) => (
              <WarningGroup
                key={group.kind}
                group={group}
                link={written ? actionLink(group.action) : null}
                onNavigate={onNavigate}
              />
            ))}
          </ul>
          {resolvable && !written ? (
            <p className="hint">Once the import is done, each one you can fix links to where.</p>
          ) : null}
        </Callout>
      ) : null}

      {notes.length > 0 ? (
        <ul className="import-warnings import-warnings--notes">
          {notes.map((group) => (
            <WarningGroup key={group.kind} group={group} link={null} />
          ))}
        </ul>
      ) : null}
    </>
  )
}

function WarningGroup({
  group,
  link,
  onNavigate,
}: {
  group: ImportWarningGroup
  link: { label: string; to: string } | null
  onNavigate?: () => void
}) {
  return (
    <li className="import-warning">
      <details>
        <summary>
          {group.summary}
          <span className="import-warning__count">{formatCount(group.count)}</span>
        </summary>
        <ul className="import__problems">
          {group.items.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      </details>
      {link ? (
        <Button asChild size="sm" variant="ghost">
          <Link to={link.to} onClick={onNavigate}>
            {link.label}
          </Link>
        </Button>
      ) : null}
    </li>
  )
}
