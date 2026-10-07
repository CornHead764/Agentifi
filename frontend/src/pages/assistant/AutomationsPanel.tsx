import { Bot, ChevronDown, Pencil, Play, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Callout,
  Card,
  ConfirmDialog,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  EmptyState,
  IconButton,
  List,
  ListRow,
  Switch,
  useConfirm,
  useToast,
} from '@/components/ui'
import type { AssistantStatus } from '@/lib/clients/assistant'
import {
  RUN_TONE,
  describeDecision,
  describeFiredBy,
  describeMode,
  describeTrigger,
  useAutomationRuns,
  useAutomationTemplates,
  useAutomations,
  useDeleteAutomation,
  useRunAutomation,
  useUpdateAutomation,
  runRequest,
  type Automation,
  type RunOptions,
  type AutomationRun,
} from '@/lib/clients/automations'
import type { Uuid } from '@/lib/transactions/types'

import { AutomationEditor, type EditorSeed } from './AutomationEditor'
import { TurnOnChangesButton } from './connection'
import { RunDialog } from './RunDialog'
import { RunOptionsDialog } from './RunOptionsDialog'
import { SUGGEST_TEMPLATE_KEY } from './suggestCategories'
import { plural, timeAgo } from '@/lib/format'

/**
 * The assistant, running on its own. Two cards: what is set up to run, and
 * what has run. What the runs proposed is decided on the register's rows.
 * The editor's seed is the caller's, because the page header's
 * `NewAutomationMenu` opens it too.
 */
export function AutomationsPanel({
  status,
  editing,
  onEditingChange: setEditing,
}: {
  status: AssistantStatus
  editing: EditorSeed | null
  onEditingChange: (seed: EditorSeed | null) => void
}) {
  const [viewingRun, setViewingRun] = useState<Uuid | null>(null)

  return (
    <div className="stack">
      <AutomationList
        status={status}
        onEdit={(automation) => setEditing({ automation })}
        onNew={setEditing}
        onOpenRun={setViewingRun}
      />
      <RecentRuns onOpenRun={setViewingRun} />

      <AutomationEditor
        seed={editing}
        status={status}
        onClose={() => setEditing(null)}
        onOpenRun={(id) => {
          setEditing(null)
          setViewingRun(id)
        }}
      />
      <RunDialog
        runId={viewingRun}
        status={status}
        onClose={() => setViewingRun(null)}
      />
    </div>
  )
}

function AutomationList({
  status,
  onEdit,
  onNew,
  onOpenRun,
}: {
  status: AssistantStatus
  onEdit: (automation: Automation) => void
  onNew: (seed: EditorSeed) => void
  onOpenRun: (id: Uuid) => void
}) {
  const automations = useAutomations()
  const templates = useAutomationTemplates()
  const suggestTemplate = templates.data?.find((one) => one.key === SUGGEST_TEMPLATE_KEY)
  const update = useUpdateAutomation()
  const remove = useConfirm(useDeleteAutomation(), {
    variables: (automation: Automation) => automation.id,
  })
  const run = useRunAutomation()
  const [running, setRunning] = useState<Uuid | null>(null)
  const [choosing, setChoosing] = useState<Automation | null>(null)
  const { show } = useToast()

  const runNow = (automation: Automation, options: RunOptions) => {
    setChoosing(null)
    setRunning(automation.id)
    run.mutate(
      runRequest(automation, options),
      {
        onSuccess: (result) => {
          // Several rows are queued for the worker rather than run inside the
          // request, so there is no run to open — only a count to report.
          if (Array.isArray(result)) {
            show({
              title: `Queued ${plural(result.length, 'run')}`,
              description: 'They appear under Recent runs as the worker reaches them.',
              tone: 'success',
            })
            return
          }
          onOpenRun(result.id)
        },
        onSettled: () => setRunning(null),
      },
    )
  }

  const wantsChanges = (automations.data ?? []).some((one) => one.mode !== 'observe')

  return (
    <Card
      title={
        <>
          <Bot size={16} aria-hidden="true" /> Automations
        </>
      }
      subtitle="The assistant, run by a trigger instead of a question."
    >
      {!status.allow_writes && wantsChanges ? (
        <Callout tone="warning" actions={<TurnOnChangesButton status={status} />}>
          Changes are off for the assistant, so automations that propose or apply them will fail.
        </Callout>
      ) : null}

      <QueryBoundary
        query={automations}
        rows={3}
        empty={(rows) =>
          rows.length === 0 ? (
            <EmptyState
              title="No automations yet"
              body="Suggest categories is added for you the first time rows arrive or you ask for suggestions in Transactions. It suggests a category for each row for you to accept, and changes nothing on its own."
              action={
                suggestTemplate ? (
                  <Button size="sm" onClick={() => onNew({ template: suggestTemplate })}>
                    <Plus size={13} aria-hidden="true" /> Set up Suggest categories
                  </Button>
                ) : null
              }
            />
          ) : null
        }
      >
        {(rows) => (
          <List>
            {rows.map((one) => (
              <ListRow
                key={one.id}
                className={one.is_enabled ? undefined : 'automation-row--off'}
                title={<span title={one.description || one.name}>{one.name}</span>}
                badge={
                  <>
                    <Badge tone={one.mode === 'apply' ? 'warning' : one.mode === 'observe' ? 'neutral' : 'accent'}>
                      {describeMode(one.mode)}
                    </Badge>
                    {one.pending_actions > 0 ? (
                      <>
                        {' '}
                        <Badge tone="accent" count>
                          {one.pending_actions}
                        </Badge>
                      </>
                    ) : null}
                  </>
                }
                sub={<span title={describeAutomation(one)}>{describeAutomation(one)}</span>}
                actions={
                  <>
                    <Switch
                      label={one.is_enabled ? 'On' : 'Off'}
                      labelPosition="before"
                      checked={one.is_enabled}
                      disabled={update.isPending}
                      onCheckedChange={(next) => update.mutate({ id: one.id, is_enabled: next })}
                    />
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={running === one.id}
                      onClick={() => setChoosing(one)}
                    >
                      <Play size={13} aria-hidden="true" /> {running === one.id ? 'Running…' : 'Run'}
                    </Button>
                    <IconButton label={`Edit ${one.name}`} variant="ghost" size="sm" onClick={() => onEdit(one)}>
                      <Pencil size={13} />
                    </IconButton>
                    <IconButton
                      label={`Delete ${one.name}`}
                      variant="ghost"
                      size="sm"
                      disabled={remove.dialog.pending}
                      onClick={() => remove.ask(one)}
                    >
                      <Trash2 size={13} />
                    </IconButton>
                  </>
                }
              />
            ))}
          </List>
        )}
      </QueryBoundary>

      <RunOptionsDialog automation={choosing} onRun={runNow} onClose={() => setChoosing(null)} />

      <ConfirmDialog
        {...remove.dialog}
        title={remove.target ? `Delete ${remove.target.name}?` : 'Delete automation?'}
        description="Deletes its run history. Changes already applied stay."
        confirmLabel="Delete automation"
      />
    </Card>
  )
}

function RecentRuns({ onOpenRun }: { onOpenRun: (id: Uuid) => void }) {
  const runs = useAutomationRuns(50)
  const automations = useAutomations()
  const names = new Map((automations.data ?? []).map((one) => [one.id, one.name]))

  return (
    <Card title="Recent runs" subtitle="Newest first. Open one for the full record.">
      <QueryBoundary
        query={runs}
        rows={3}
        empty={(rows) =>
          rows.length === 0 ? (
            <EmptyState title="Nothing has run yet" body="Runs appear here as automations fire." />
          ) : null
        }
      >
        {(rows) => (
          <List>
            {rows.map((one) => (
              <RunRow key={one.id} run={one} name={names.get(one.automation_id) ?? 'Deleted automation'} onOpen={onOpenRun} />
            ))}
          </List>
        )}
      </QueryBoundary>
    </Card>
  )
}

function RunRow({
  run,
  name,
  onOpen,
}: {
  run: AutomationRun
  name: string
  onOpen: (id: Uuid) => void
}) {
  const about = [
    timeAgo(run.queued_at, 'long'),
    run.subject || describeFiredBy(run.fired_by),
    run.actions > 0 ? plural(run.actions, 'change') : '',
    describeDecision(run),
  ]
    .filter(Boolean)
    .join(' · ')
  return (
    <ListRow
      title={
        <>
          {name}
          {run.blind ? (
            <span className="muted"> · blind</span>
          ) : run.dry_run ? (
            <span className="muted"> · dry run</span>
          ) : null}
        </>
      }
      sub={<span title={about}>{about}</span>}
      figures={<Badge tone={RUN_TONE[run.status]}>{run.status}</Badge>}
      onSelect={() => onOpen(run.id)}
    />
  )
}

/** The line under an automation's name: its trigger, model and last run. */
function describeAutomation(one: Automation): string {
  return [
    `Runs ${describeTrigger(one)}`,
    one.model,
    one.runs > 0 ? `${plural(one.runs, 'run')}, last ${timeAgo(one.last_run_at, 'long')}` : 'never run',
    one.last_status === 'failed' ? 'last run failed' : '',
  ]
    .filter(Boolean)
    .join(' · ')
}

/**
 * The page header's primary action on the Automations tab: a template to
 * start from, or a blank form.
 */
export function NewAutomationMenu({ onNew }: { onNew: (seed: EditorSeed) => void }) {
  const templates = useAutomationTemplates()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="primary" size="sm">
          <Plus size={13} aria-hidden="true" /> New automation <ChevronDown size={13} aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {(templates.data ?? []).map((template) => (
          <DropdownMenuItem key={template.key} onSelect={() => onNew({ template })}>
            {template.name}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => onNew({})}>Start from scratch</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
