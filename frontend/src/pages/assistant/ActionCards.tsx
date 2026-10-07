import {
  ArrowUpRight,
  Check,
  CheckCheck,
  CircleAlert,
  Pencil,
  RotateCcw,
  SquarePen,
  X,
} from 'lucide-react'
import { Fragment, useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { Money } from '@/components/Money'
import { useMoneyText } from '@/components/moneyText'
import { Badge, Button, Input, Spinner } from '@/components/ui'
import type { BadgeTone } from '@/components/ui'
import { describeAbout } from '@/lib/assistant/change'
import {
  useApplyAction,
  useApplyManyActions,
  useDiscardAction,
  useDiscardManyActions,
  type AssistantAction,
  type PreviewField,
} from '@/lib/clients/assistant'
import { plural } from '@/lib/format'
import { parseMoney } from '@/lib/money'
import { registerLinkFor } from '@/lib/transactions/links'
import { useAccounts, useCategories, useTags } from '@/lib/transactions/queries'
import type { Uuid } from '@/lib/transactions/types'

import {
  cardLink,
  cardNames,
  cardPhase,
  decidable,
  failureText,
  groupTally,
  ruleView,
  type CardPhase,
} from './cards'
import { ChangeReview, ROW_CATEGORY } from './ChangeReview'

/**
 * A proposed change, and the one decision it asks for. Six states decide
 * everything on it:
 *
 *   - **proposed**: what it would do, with Accept and Decline;
 *   - **running**: accepted and in flight, with nothing to press;
 *   - **done**: applied, with a way to the thing it made;
 *   - **failed**: the app refused it, why, and Try again or Decline;
 *   - **declined**: kept in the thread, faded, with the reason given;
 *   - **simulated**: a dry run's record, with nothing to press ever.
 *
 * What it would do is drawn from the request itself, never from the model's
 * description of it, so what is reviewed and what runs cannot drift apart.
 */

const PHASE_LABEL: Record<CardPhase, string> = {
  proposed: 'Proposed',
  running: 'Applying…',
  done: 'Done',
  failed: 'Failed',
  declined: 'Declined',
  simulated: 'Would have done · dry run',
}

const PHASE_TONE: Record<CardPhase, BadgeTone> = {
  proposed: 'accent',
  running: 'neutral',
  done: 'income',
  failed: 'expense',
  declined: 'neutral',
  simulated: 'accent',
}

/** The tools whose card is the category review rather than a field list. */
const REVIEWED_TOOLS = new Set(['update_transaction', 'create_transaction', 'split_transaction'])

export function ActionCard({
  action,
  conversationId,
  onDecided,
  subject,
}: {
  action: AssistantAction
  conversationId: Uuid
  /** Called once a decision has landed, for a list that has to refetch itself. */
  onDecided?: () => void
  /** What the change is about, when the card is shown away from its thread. */
  subject?: string
}) {
  const apply = useApplyAction()
  const moneyText = useMoneyText()
  const discard = useDiscardAction()
  const [chosen, setChosen] = useState<Record<number, Uuid | null>>({})
  const [declining, setDeclining] = useState(false)
  const busy = apply.isPending || discard.isPending
  const phase: CardPhase = apply.isPending ? 'running' : cardPhase(action.status)
  const settled = { onSettled: () => onDecided?.() }
  const about = subject ?? describeAbout(action, moneyText).line
  const link = cardLink(action)
  const editLink =
    action.about && phase !== 'declined'
      ? registerLinkFor(
          { id: action.about.transaction_id, account_id: action.about.account_id, date: action.about.date },
          { open: true },
        )
      : null

  const accept = () =>
    apply.mutate({ id: action.id, conversationId, overrides: applyOverrides(chosen) }, settled)

  return (
    <div className="chat__change" data-status={action.status} data-phase={phase}>
      <p className="chat__change-head">
        <Pencil size={13} aria-hidden="true" />
        <span className="chat__change-title">{action.summary}</span>
        <Badge tone={PHASE_TONE[phase]}>{PHASE_LABEL[phase]}</Badge>
      </p>
      {about ? <p className="muted chat__change-subject">{about}</p> : null}

      <ChangeBody
        action={action}
        chosen={chosen}
        editable={decidable(action) && !busy}
        onChoose={(index, id) => setChosen((current) => ({ ...current, [index]: id }))}
      />

      <details className="chat__change-fold">
        <summary>The request it will send</summary>
        <pre className="chat__change-request">
          {action.method} {action.path}
          {action.body ? `\n${JSON.stringify(action.body, null, 2)}` : ''}
        </pre>
      </details>

      <Outcome action={action} phase={phase} />

      <div className="row row--wrap chat__change-actions">
        {phase === 'proposed' || phase === 'failed' ? (
          <>
            <Button variant="primary" size="sm" disabled={busy} onClick={accept}>
              {phase === 'failed' ? (
                <>
                  <RotateCcw size={13} aria-hidden="true" /> Try again
                </>
              ) : (
                <>
                  <Check size={13} aria-hidden="true" />{' '}
                  {Object.keys(chosen).length > 0 ? 'Accept with your categories' : 'Accept'}
                </>
              )}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={busy}
              aria-expanded={declining}
              onClick={() => setDeclining(!declining)}
            >
              <X size={13} aria-hidden="true" /> Decline
            </Button>
          </>
        ) : null}
        {phase === 'running' ? (
          <span className="chat__change-running" role="status">
            <Spinner size={13} /> Applying…
          </span>
        ) : null}
        {link ? (
          <Button asChild variant="ghost" size="sm">
            <Link to={link.to}>
              <ArrowUpRight size={13} aria-hidden="true" /> {link.label}
            </Link>
          </Button>
        ) : null}
        {/* Offered on a settled card too: "it applied, and now I want to
            change something else about that row" is the same errand. */}
        {editLink && !link ? (
          <Button asChild variant="ghost" size="sm">
            <Link to={editLink}>
              <SquarePen size={13} aria-hidden="true" /> Edit transaction
            </Link>
          </Button>
        ) : null}
      </div>

      {declining && decidable(action) ? (
        <DeclineForm
          busy={busy}
          onCancel={() => setDeclining(false)}
          onDecline={(reason) =>
            discard.mutate(
              { id: action.id, conversationId, reason },
              {
                onSettled: () => {
                  setDeclining(false)
                  onDecided?.()
                },
              },
            )
          }
        />
      ) : null}
    </div>
  )
}

/**
 * The cards one bulk proposal made, as one card. Each row is still its own
 * request, settled on its own, so a refused row says so on its line; the
 * shared category is said once above the list.
 */
export function ActionGroupCard({
  actions,
  conversationId,
}: {
  actions: readonly AssistantAction[]
  conversationId: Uuid
}) {
  const applyMany = useApplyManyActions()
  const discardMany = useDiscardManyActions()
  const [declining, setDeclining] = useState(false)
  const busy = applyMany.isPending || discardMany.isPending
  const first = actions[0]
  const open = actions.filter(decidable)
  const tally = groupTally(actions)
  const phase: CardPhase =
    busy || tally.running > 0
      ? 'running'
      : open.length > 0
        ? tally.failed > 0 && tally.proposed === 0
          ? 'failed'
          : 'proposed'
        : tally.done > 0
          ? 'done'
          : tally.simulated > 0
            ? 'simulated'
            : 'declined'
  const summary = first.preview?.group_summary ?? first.summary
  const names = useKnownNames(first)

  return (
    <div className="chat__change chat__change--group" data-phase={phase}>
      <p className="chat__change-head">
        <CheckCheck size={13} aria-hidden="true" />
        <span className="chat__change-title">{summary}</span>
        <Badge tone={PHASE_TONE[phase]}>
          {plural(actions.length, 'change')}
        </Badge>
      </p>
      <PreviewFields fields={first.preview?.fields ?? []} names={names} />

      <ul className="chat__change-rows">
        {actions.map((action) => {
          const rowPhase = busy && decidable(action) ? 'running' : cardPhase(action.status)
          return (
            <li key={action.id} data-phase={rowPhase}>
              <PhaseIcon phase={rowPhase} />
              <span className="chat__change-row-text">{action.summary}</span>
              {rowPhase === 'failed' ? (
                <span className="chat__change-result">{failureText(action.result)}</span>
              ) : null}
            </li>
          )
        })}
      </ul>

      <p className="muted chat__change-tally">{describeTally(tally)}</p>

      <div className="row row--wrap chat__change-actions">
        {open.length > 0 && !busy ? (
          <>
            <Button
              variant="primary"
              size="sm"
              onClick={() => applyMany.mutate({ ids: open.map((one) => one.id), conversationId })}
            >
              <CheckCheck size={13} aria-hidden="true" /> Accept all {open.length}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              aria-expanded={declining}
              onClick={() => setDeclining(!declining)}
            >
              <X size={13} aria-hidden="true" /> Decline all
            </Button>
          </>
        ) : null}
        {busy ? (
          <span className="chat__change-running" role="status">
            <Spinner size={13} /> Applying…
          </span>
        ) : null}
      </div>
      {declining && open.length > 0 ? (
        <DeclineForm
          busy={busy}
          onCancel={() => setDeclining(false)}
          onDecline={(reason) =>
            discardMany.mutate(
              { ids: open.map((one) => one.id), conversationId, reason },
              { onSettled: () => setDeclining(false) },
            )
          }
        />
      ) : null}
    </div>
  )
}

function describeTally(tally: ReturnType<typeof groupTally>): string {
  const parts: string[] = []
  if (tally.proposed > 0) parts.push(`${tally.proposed} waiting`)
  if (tally.running > 0) parts.push(`${tally.running} applying`)
  if (tally.done > 0) parts.push(`${tally.done} applied`)
  if (tally.failed > 0) parts.push(`${tally.failed} refused`)
  if (tally.declined > 0) parts.push(`${tally.declined} declined`)
  if (tally.simulated > 0) parts.push(`${tally.simulated} dry run`)
  return parts.join(' · ')
}

function PhaseIcon({ phase }: { phase: CardPhase }) {
  switch (phase) {
    case 'running':
      return <Spinner size={12} label="Applying" />
    case 'done':
      return <Check size={12} aria-label="Applied" />
    case 'failed':
      return <CircleAlert size={12} aria-label="Refused" />
    case 'declined':
      return <X size={12} aria-label="Declined" />
    default:
      return <span className="chat__change-dot" aria-label="Waiting" />
  }
}

/**
 * Why somebody said no, for the model to hear with their next message.
 * Optional: declining without a word is a fine answer too.
 */
function DeclineForm({
  busy,
  onDecline,
  onCancel,
}: {
  busy: boolean
  onDecline: (reason: string) => void
  onCancel: () => void
}) {
  const [reason, setReason] = useState('')
  return (
    <form
      className="chat__decline"
      onSubmit={(event) => {
        event.preventDefault()
        onDecline(reason)
      }}
    >
      <Input
        aria-label="Why not? (optional)"
        placeholder="Why not? The assistant reads this. (optional)"
        value={reason}
        disabled={busy}
        autoFocus
        onChange={(event) => setReason(event.target.value)}
      />
      <div className="row row--wrap chat__change-actions">
        <Button type="submit" variant="secondary" size="sm" disabled={busy}>
          Decline
        </Button>
        <Button type="button" variant="ghost" size="sm" disabled={busy} onClick={onCancel}>
          Keep it
        </Button>
      </div>
    </form>
  )
}

/** What happened, once something has: the refusal, or the reason declined. */
function Outcome({ action, phase }: { action: AssistantAction; phase: CardPhase }) {
  if (phase === 'failed' && action.result !== '') {
    return (
      <p className="chat__change-result" role="alert">
        <CircleAlert size={13} aria-hidden="true" /> {failureText(action.result)}
      </p>
    )
  }
  if (phase === 'declined' && action.decline_reason) {
    return <p className="muted chat__change-subject">You said: “{action.decline_reason}”</p>
  }
  return null
}

/** The part of the card that says what the change does. */
function ChangeBody({
  action,
  chosen,
  editable,
  onChoose,
}: {
  action: AssistantAction
  chosen: Readonly<Record<number, Uuid | null>>
  editable: boolean
  onChoose: (index: number, id: Uuid | null) => void
}) {
  const names = useKnownNames(action)
  const categories = useCategories()
  const tags = useTags()
  const rule = useMemo(
    () =>
      ruleView(action, { categories: categories.data ?? [], tags: tags.data ?? [] }, names),
    [action, categories.data, tags.data, names],
  )

  if (rule) return <RuleProposal rule={rule} />
  if (REVIEWED_TOOLS.has(action.tool)) {
    return <ChangeReview action={action} chosen={chosen} editable={editable} onChoose={onChoose} />
  }
  return <PreviewFields fields={action.preview?.fields ?? []} names={names} />
}

/** Names from the preview over names this page already has loaded. */
function useKnownNames(action: Pick<AssistantAction, 'preview'>): Map<string, string> {
  const categories = useCategories()
  const tags = useTags()
  const accounts = useAccounts()
  return useMemo(
    () =>
      cardNames(action, [
        ...(categories.data ?? []),
        ...(tags.data ?? []),
        ...(accounts.data ?? []),
      ]),
    [action, categories.data, tags.data, accounts.data],
  )
}

/**
 * A proposed rule, the way the Rules page lists one: its name, the chips for
 * what it matches, and the chips for what it then does.
 */
function RuleProposal({ rule }: { rule: { name: string; conditions: string[]; then: string[] } }) {
  return (
    <div className="rule-proposal">
      {rule.name ? <p className="rule-proposal__name">{rule.name}</p> : null}
      <RuleChips label="If a transaction matches" chips={rule.conditions} />
      <RuleChips label="Then" chips={rule.then} />
    </div>
  )
}

function RuleChips({ label, chips }: { label: string; chips: readonly string[] }) {
  if (chips.length === 0) return null
  return (
    <div className="rule-proposal__row">
      <span className="hint hint--faint">{label}</span>
      <span className="chips chips--wrap">
        {chips.map((text) => (
          <Badge key={text}>{text}</Badge>
        ))}
      </span>
    </div>
  )
}

/** The request's fields as the server named them, for any card without its own view. */
function PreviewFields({
  fields,
  names,
}: {
  fields: readonly PreviewField[]
  names?: ReadonlyMap<string, string>
}) {
  if (fields.length === 0) return null
  return (
    <div className="change-review__fields">
      {fields.map((field, index) => (
        <Fragment key={`${field.label}-${index}`}>
          <span className="hint hint--faint">{field.label}</span>
          <span className="change-review__value">{renderValue(field, names)}</span>
        </Fragment>
      ))}
    </div>
  )
}

function renderValue(field: PreviewField, names?: ReadonlyMap<string, string>): ReactNode {
  if (field.kind === 'money') {
    try {
      return <Money value={parseMoney(field.value)} tone="neutral" />
    } catch {
      return field.value
    }
  }
  return names?.get(field.value) ?? field.value
}

/**
 * The categories a person changed, as the Apply request carries them. Only
 * what they actually moved, so a card applied unchanged records no
 * correction. `null` is the empty category and travels as `""`; omitting the
 * field means "leave the model's choice alone".
 */
function applyOverrides(chosen: Readonly<Record<number, Uuid | null>>) {
  const entries = Object.entries(chosen)
  if (entries.length === 0) return undefined
  const overrides: { category_id?: string; split_categories?: { index: number; category_id: string }[] } =
    {}
  for (const [key, id] of entries) {
    const index = Number(key)
    if (index === ROW_CATEGORY) {
      overrides.category_id = id ?? ''
      continue
    }
    overrides.split_categories = [...(overrides.split_categories ?? []), { index, category_id: id ?? '' }]
  }
  return overrides
}
