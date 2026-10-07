import { CircleCheck, CircleMinus, FileUp, X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import { Button, Card } from '@/components/ui'
import {
  useSaveSetupGuide,
  withSkipped,
  type SetupGuide as Guide,
  type SetupStep,
  type SetupStepId,
} from '@/lib/clients/setupGuide'
import { useCurrentSpace } from '@/lib/clients/spaces'

import { CONNECT_PATH } from './paths'
import { SimplifiImportDialog } from './SimplifiImportDialog'

interface StepText {
  title: string
  body: ReactNode
  /** What a skipped step says in place of its body. */
  skipped: string
  /** The skip button's words; none for a step that cannot be skipped. */
  skip?: string
}

const STEP_TEXT: Record<SetupStepId, StepText> = {
  import: {
    title: 'Coming from Quicken Simplifi? Import your history first',
    body:
      'Accounts, transactions, categories, rules and the spending plan come across from the ' +
      'export. Importing before SimpleFIN lets each imported account be matched to its bank ' +
      'feed in step 3, so its history carries on without duplicates.',
    skipped:
      'Skipped. An import fills an empty space only; to bring history in later, create a new ' +
      'space for it under Spaces & sharing.',
    skip: 'I’m not coming from Simplifi',
  },
  connect: {
    title: 'Connect your banks with SimpleFIN',
    body:
      'One SimpleFIN Bridge connection covers every bank linked there. Its accounts wait to be ' +
      'matched before anything syncs, so imported accounts can take them over.',
    skipped: 'Skipped. Accounts can be added by hand or from statement files instead.',
    skip: 'I’ll add accounts another way',
  },
  match: {
    title: 'Match accounts and run the first sync',
    body:
      'On Settings, Accounts, pair each SimpleFIN account with an imported one or create it new, ' +
      'then finish to start syncing. The first sync carries on from where the history ends.',
    skipped: 'Skipped along with connecting SimpleFIN.',
  },
  assistant: {
    title: 'Set up the assistant',
    body:
      'Point it at a model you choose, hosted or your own, and it suggests categories for new ' +
      'transactions for you to accept.',
    skipped: 'Skipped. The Assistant page sets it up any time.',
    skip: 'Not now',
  },
  bills: {
    title: 'Add bill providers',
    body:
      'Sign in to utilities and card issuers so statements, amounts and due dates arrive on ' +
      'their own.',
    skipped: 'Skipped. Settings, Bill providers adds one any time.',
    skip: 'Not now',
  },
  backups: {
    title: 'Save a backup key',
    body:
      'The server writes a backup every night, unencrypted until a key is saved. Keep the ' +
      'private half somewhere other than this server.',
    skipped: 'Skipped. Settings, Server admin, Backups saves one any time.',
    skip: 'Not now',
  },
}

/** Where an open step is done, for the steps done on another page. */
const STEP_LINKS: Partial<Record<SetupStepId, { to: string; label: string }>> = {
  connect: { to: CONNECT_PATH, label: 'Connect SimpleFIN' },
  match: { to: CONNECT_PATH, label: 'Match accounts' },
  assistant: { to: '/assistant', label: 'Set up the assistant' },
  bills: { to: '/settings/bills', label: 'Add a bill provider' },
  backups: { to: '/settings/admin', label: 'Open backups' },
}

/**
 * The steps a new space goes through, checked off from what the ledger shows.
 * The import comes first because the accounts it brings are what SimpleFIN's
 * are matched to. Hidden here, it comes back from the Help menu.
 */
export function SetupGuide({ guide }: { guide: Guide }) {
  const save = useSaveSetupGuide()
  const required = guide.steps.filter((step) => !step.optional)
  const optional = guide.steps.filter((step) => step.optional)
  const finished = guide.steps.filter((step) => step.state !== 'open').length

  const skip = (id: SetupStepId, skipping: boolean) =>
    save.mutate({ skipped: withSkipped(guide, id, skipping) })

  return (
    <Card
      className="setup-guide"
      title="Set up this space"
      subtitle={`${finished} of ${guide.steps.length} done`}
      actions={
        <Button
          size="sm"
          variant="ghost"
          disabled={save.isPending}
          onClick={() => save.mutate({ dismissed: true })}
        >
          <X size={13} aria-hidden="true" /> Hide guide
        </Button>
      }
    >
      <ol className="setup-guide__steps">
        {required.map((step, index) => (
          <GuideStep
            key={step.id}
            step={step}
            number={index + 1}
            undoable={guide.skipped.includes(step.id)}
            busy={save.isPending}
            onSkip={skip}
          />
        ))}
      </ol>
      {optional.length > 0 ? (
        <>
          <p className="setup-guide__heading">When you are ready</p>
          <ol className="setup-guide__steps">
            {optional.map((step) => (
              <GuideStep
                key={step.id}
                step={step}
                undoable={guide.skipped.includes(step.id)}
                busy={save.isPending}
                onSkip={skip}
              />
            ))}
          </ol>
        </>
      ) : null}
      <p className="setup-guide__foot muted">
        Hidden, the guide comes back from the Help menu at the top of every page.
      </p>
    </Card>
  )
}

function GuideStep({
  step,
  number,
  undoable,
  busy,
  onSkip,
}: {
  step: SetupStep
  number?: number
  undoable: boolean
  busy: boolean
  onSkip: (id: SetupStepId, skipping: boolean) => void
}) {
  const text = STEP_TEXT[step.id]
  const link = STEP_LINKS[step.id]
  const open = step.state === 'open'

  return (
    <li className="setup-guide__step" data-state={step.state}>
      <span className="setup-guide__marker" aria-hidden="true">
        {step.state === 'done' ? (
          <CircleCheck size={18} />
        ) : step.state === 'skipped' ? (
          <CircleMinus size={18} />
        ) : number !== undefined ? (
          number
        ) : null}
      </span>
      <div className="setup-guide__content">
        <p className="setup-guide__title">
          {text.title}
          <span className="visually-hidden">
            {step.state === 'done' ? ' (done)' : step.state === 'skipped' ? ' (skipped)' : ''}
          </span>
        </p>
        {open ? (
          <p className="setup-guide__body">{text.body}</p>
        ) : step.state === 'skipped' ? (
          <p className="setup-guide__body">{text.skipped}</p>
        ) : null}
        {open ? (
          <div className="setup-guide__actions">
            {step.id === 'import' ? <ImportAction /> : null}
            {link ? (
              <Button asChild size="sm" variant={step.optional ? 'secondary' : 'primary'}>
                <Link to={link.to}>{link.label}</Link>
              </Button>
            ) : null}
            {text.skip ? (
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => onSkip(step.id, true)}>
                {text.skip}
              </Button>
            ) : null}
          </div>
        ) : undoable ? (
          <div className="setup-guide__actions">
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => onSkip(step.id, false)}>
              Undo skip
            </Button>
          </div>
        ) : null}
      </div>
    </li>
  )
}

/** The Simplifi import is the owner's to run; anybody else is told who can. */
function ImportAction() {
  const space = useCurrentSpace()
  const [importing, setImporting] = useState(false)
  if (!space.data?.is_owner) {
    return <p className="setup-guide__body">The space’s owner or an admin can run the import.</p>
  }
  return (
    <>
      <Button size="sm" variant="primary" onClick={() => setImporting(true)}>
        <FileUp size={13} aria-hidden="true" />
        Import from Simplifi
      </Button>
      <SimplifiImportDialog open={importing} onOpenChange={setImporting} matchPath={CONNECT_PATH} />
    </>
  )
}

/**
 * The way back to a hidden guide, in the Help menu: shown again on the
 * dashboard, where it stays until hidden or finished.
 */
export function SetupGuideLink() {
  const space = useCurrentSpace()
  const save = useSaveSetupGuide()
  const navigate = useNavigate()
  if (!space.data?.can_write) return null
  return (
    <Button
      size="sm"
      variant="ghost"
      className="setup-guide__reopen"
      disabled={save.isPending}
      onClick={() => save.mutate({ dismissed: false }, { onSuccess: () => navigate('/') })}
    >
      Show the setup guide
    </Button>
  )
}
