import { useState } from 'react'

import { InfoTip } from '@/components/InfoTip'
import {
  Button,
  Card,
  ConfirmDialog,
  Dialog,
  DialogContent,
  Field,
  Input,
  List,
  ListRow,
  OptionSelect,
  Switch,
  useConfirm,
} from '@/components/ui'
import { describeProbe } from '@/lib/assistant/probe'
import {
  useAssistantStatus,
  useForgetConnection,
  useSaveConnection,
  useTestConnection,
  type AssistantStatus,
  type ConnectionTest,
} from '@/lib/clients/assistant'
import { useOwnsSpace } from '@/lib/clients/spaces'

/**
 * The provider, and what it is allowed to do. The two change switches sit on
 * the Assistant page itself, because whoever turns them on approves what they
 * produce.
 */

const SETUP_SUBTITLE =
  'Any OpenAI-compatible endpoint, hosted or on your network. Nothing is sent until this is filled in.'

/** The first-run form: nothing is sent anywhere until this is filled in. */
export function Setup() {
  const status = useAssistantStatus()
  const ownsSpace = useOwnsSpace()

  return (
    <>
      <Card title="Set up the assistant" subtitle={SETUP_SUBTITLE}>
        {ownsSpace ? (
          <SetupForm />
        ) : (
          <p className="muted">The space&rsquo;s owner sets up the assistant&rsquo;s model.</p>
        )}
      </Card>

      {status.data ? <ToolList tools={status.data.tools} /> : null}
    </>
  )
}

/** The provider's three fields and a save, wherever the setup is offered. */
function SetupForm({
  onSaved,
}: {
  onSaved?: () => void
}) {
  const save = useSaveConnection()
  const [form, setForm] = useState({ base_url: '', model: '', api_key: '' })

  return (
    <>
      <Field label="Base URL" hint="e.g. https://api.openai.com/v1 or http://10.0.0.5:11434/v1">
        <Input
          value={form.base_url}
          placeholder="https://api.openai.com/v1"
          onChange={(event) => setForm({ ...form, base_url: event.target.value })}
        />
      </Field>
      <Field label="Model">
        <Input
          value={form.model}
          placeholder="gpt-4o-mini"
          onChange={(event) => setForm({ ...form, model: event.target.value })}
        />
      </Field>
      <Field label="API key" hint="Stored encrypted, never shown again.">
        <Input
          type="password"
          value={form.api_key}
          placeholder="Leave blank if your endpoint needs none"
          onChange={(event) => setForm({ ...form, api_key: event.target.value })}
        />
      </Field>
      <Button
        variant="primary"
        disabled={form.base_url.trim() === '' || form.model.trim() === '' || save.isPending}
        onClick={() => save.mutate(form, { onSuccess: () => onSaved?.() })}
      >
        {save.isPending ? 'Saving…' : 'Save'}
      </Button>
    </>
  )
}

/**
 * The assistant made ready from wherever it was found not to be: the
 * first-run form when nothing is configured, the master switch when a stored
 * connection is off. Closes itself once the save lands.
 */
export function AssistantSetupDialog({
  open,
  onClose,
}: {
  open: boolean
  onClose: () => void
}) {
  const status = useAssistantStatus()
  const save = useSaveConnection()
  const stored = status.data?.configured ? status.data : null

  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent
        title={stored ? 'Turn the assistant on' : 'Set up the assistant'}
        description={
          stored
            ? `Set up with ${stored.model}, switched off. Turning it on changes nothing else.`
            : SETUP_SUBTITLE
        }
      >
        {stored ? (
          <Button
            variant="primary"
            disabled={save.isPending}
            onClick={() =>
              save.mutate(
                {
                  base_url: stored.base_url,
                  model: stored.model,
                  name: stored.name,
                  is_enabled: true,
                },
                { onSuccess: onClose },
              )
            }
          >
            {save.isPending ? 'Turning on…' : 'Turn it on'}
          </Button>
        ) : (
          <SetupForm onSaved={onClose} />
        )}
      </DialogContent>
    </Dialog>
  )
}

/** "Let it propose changes", as one button beside the thing it would unblock: the same save the switch makes. */
export function TurnOnChangesButton({
  status,
}: {
  status: AssistantStatus
}) {
  const save = useSaveConnection()
  return (
    <Button
      variant="secondary"
      size="sm"
      disabled={save.isPending}
      onClick={() =>
        save.mutate({
          base_url: status.base_url,
          model: status.model,
          name: status.name,
          allow_writes: true,
        })
      }
    >
      {save.isPending ? 'Turning on…' : 'Let it propose changes'}
    </Button>
  )
}

/**
 * The connection form and what the assistant can do, over the chat, for
 * changing which model answers without leaving the conversation.
 */
export function ConnectionDialog({
  status,
  open,
  onClose,
}: {
  status: AssistantStatus
  open: boolean
  onClose: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent
        wide
        title="Connection"
        description="Where questions are sent. Nothing about this space goes anywhere else."
      >
        <EnabledSwitch status={status} />
        <ConnectionFields status={status} />
        <ToolList tools={status.tools} />
      </DialogContent>
    </Dialog>
  )
}

/**
 * What the assistant can do, listed before it is switched on. The change tools
 * are shown whether or not they are on: the question is what it could do.
 */
export function ToolList({ tools }: { tools: AssistantStatus['tools'] }) {
  const reads = tools.filter((tool) => !tool.writes)
  const writes = tools.filter((tool) => tool.writes)

  return (
    <>
      <Card
        title="What it can read"
        subtitle="Plus read-only calls to this app's own API, in your space only."
      >
        <List dividers={false}>
          {reads.map((tool) => (
            <ListRow key={tool.name} title={tool.name.replace(/_/g, ' ')} sub={tool.description} wrap />
          ))}
        </List>
      </Card>

      <Card
        title="What it can propose"
        subtitle="Only with changes on. Each is a card to apply or discard, or is applied and shown afterwards with “apply without asking”."
      >
        <List dividers={false}>
          {writes.map((tool) => (
            <ListRow key={tool.name} title={tool.name.replace(/_/g, ' ')} sub={tool.description} wrap />
          ))}
        </List>
      </Card>
    </>
  )
}

/**
 * The master switch. The server refuses to run the assistant while it is off.
 * Turning it off keeps the connection, the key and the conversations.
 */
export function EnabledSwitch({
  status,
}: {
  status: AssistantStatus
}) {
  const save = useSaveConnection()
  return (
    <Switch
      label="Assistant enabled"
      labelPosition="before"
      checked={status.is_enabled}
      disabled={save.isPending}
      onCheckedChange={(next) =>
        save.mutate({
          base_url: status.base_url,
          model: status.model,
          name: status.name,
          is_enabled: next,
        })
      }
    />
  )
}

/**
 * The connection, editable after setup. The key field is blank on purpose: the
 * stored key never comes back to the browser (`has_key` is all this page is
 * told), and an empty field saves as "keep what is stored". Forgetting the
 * connection deletes the key and returns the screen to setup.
 */
function ConnectionFields({
  status,
}: {
  status: AssistantStatus
}) {
  const save = useSaveConnection()
  const forget = useConfirm(useForgetConnection())
  const test = useTestConnection()
  const [form, setForm] = useState({
    base_url: status.base_url,
    model: status.model,
    api_key: '',
    tool_call_style: status.tool_call_style,
  })
  const [probe, setProbe] = useState<ConnectionTest | null>(null)

  const dirty =
    form.base_url !== status.base_url ||
    form.model !== status.model ||
    form.api_key !== '' ||
    form.tool_call_style !== status.tool_call_style

  return (
    <>
      <Field label="Base URL">
        <Input
          value={form.base_url}
          onChange={(event) => setForm({ ...form, base_url: event.target.value })}
        />
      </Field>
      <Field label="Model">
        <Input
          value={form.model}
          onChange={(event) => setForm({ ...form, model: event.target.value })}
        />
      </Field>
      <Field
        label="API key"
        hint={
          status.has_key
            ? 'A key is stored. Leave blank to keep it, or type a new one.'
            : 'No key is stored. Leave blank if your endpoint needs none.'
        }
      >
        <Input
          type="password"
          value={form.api_key}
          placeholder={status.has_key ? '••••••••' : 'None'}
          onChange={(event) => setForm({ ...form, api_key: event.target.value })}
        />
      </Field>

      <Field
        label="Tool calls"
        hint={
          <>
            Test the connection to find out which yours needs.
            <InfoTip>
              Some local servers answer with an empty message when the model calls a tool, because
              nothing on the server parses the call. “In the prompt” describes the tools in the
              prompt and reads the call back out of the answer.
            </InfoTip>
          </>
        }
      >
        <OptionSelect
          value={form.tool_call_style}
          onValueChange={(value) => {
            if (value === 'native' || value === 'prompted') {
              setForm({ ...form, tool_call_style: value })
            }
          }}
          options={[
            { value: 'native', label: "Native (the API's tool_calls field)" },
            { value: 'prompted', label: 'In the prompt (for servers without a tool-call parser)' },
          ]}
        />
      </Field>

      <div className="import__actions">
        <Button
          variant="primary"
          disabled={
            !dirty || form.base_url.trim() === '' || form.model.trim() === '' || save.isPending
          }
          onClick={() =>
            save.mutate(
              {
                name: status.name,
                base_url: form.base_url.trim(),
                model: form.model.trim(),
                api_key: form.api_key,
                tool_call_style: form.tool_call_style,
              },
              { onSuccess: () => setForm((prev) => ({ ...prev, api_key: '' })) },
            )
          }
        >
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
        <Button
          variant="ghost"
          disabled={test.isPending || dirty}
          title={dirty ? 'Save first; the test runs against what is stored' : undefined}
          onClick={() => {
            setProbe(null)
            test.mutate(undefined, { onSuccess: setProbe })
          }}
        >
          {test.isPending ? 'Testing…' : 'Test connection'}
        </Button>
        <Button variant="danger" disabled={forget.dialog.pending} onClick={() => forget.ask()}>
          Remove connection
        </Button>
      </div>

      {probe ? (
        <p
          className="connection-probe"
          data-ok={probe.reachable && (probe.native_tool_calls || probe.prompted_tool_calls)}
        >
          {describeProbe(probe, status.tool_call_style)}
        </p>
      ) : null}

      <ConfirmDialog
        {...forget.dialog}
        title="Remove this connection?"
        description="Deletes the stored key and turns the assistant off. Conversations are kept."
        confirmLabel="Remove connection"
      />
    </>
  )
}

/**
 * The two switches that decide what the assistant may do, also mounted on the
 * Assistant page.
 *
 * The second appears only once the first is on: the server clears it whenever
 * changes go off, so showing it disabled would show a setting about to be
 * false.
 *
 * What they do is said in the line under the question box, not in a tooltip,
 * which opens neither on touch nor for a screen reader.
 *
 * Both send the base URL and model back unchanged because the endpoint takes
 * the whole connection; the key is omitted, which leaves the stored one alone.
 */
export function ChangesSwitches({
  status,
}: {
  status: AssistantStatus
}) {
  const save = useSaveConnection()
  const update = (changes: { allow_writes?: boolean; apply_without_asking?: boolean }) =>
    save.mutate({
      base_url: status.base_url,
      model: status.model,
      name: status.name,
      ...changes,
    })

  return (
    <span className="assistant__switches">
      <Switch
        label="Let it propose changes"
        labelPosition="before"
        checked={status.allow_writes}
        disabled={save.isPending}
        onCheckedChange={(next) => update({ allow_writes: next })}
      />
      {status.allow_writes ? (
        <Switch
          label="Apply without asking"
          labelPosition="before"
          checked={status.apply_without_asking}
          disabled={save.isPending}
          onCheckedChange={(next) => update({ apply_without_asking: next })}
        />
      ) : null}
    </span>
  )
}
