import { useState } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import { Badge, Button, Card, Field, Input, Switch, useToast } from '@/components/ui'
import {
  draftValue,
  serverSettingsBody,
  useSaveServerSettings,
  useServerSettings,
  type ServerSetting,
  type SettingsDraft,
} from '@/lib/clients/admin'

import { canReset, groupSettings, settingNote } from './serverText'

/**
 * The environment settings an administrator may change from here. The order
 * is the server's: a variable set in the environment or .env wins and shows
 * read-only, then what is saved here, then the default. Secrets are not in
 * the list.
 */
export function SettingsCard() {
  const settings = useServerSettings()
  return (
    <Card
      title="Settings"
      subtitle="The environment and .env win over this screen, and this screen over the defaults."
    >
      <QueryBoundary query={settings} rows={6}>
        {({ settings: list }) => <SettingsForm settings={list} />}
      </QueryBoundary>
    </Card>
  )
}

function SettingsForm({ settings }: { settings: ServerSetting[] }) {
  const { show } = useToast()
  const save = useSaveServerSettings()
  const [draft, setDraft] = useState<SettingsDraft>({})
  const edit = (key: string, value: string | null) =>
    setDraft((previous) => ({ ...previous, [key]: value }))
  const changed = Object.keys(draft).length > 0

  return (
    <div className="stack">
      {groupSettings(settings).map(([group, members]) => {
        const fields = members.filter((setting) => setting.kind !== 'toggle')
        return (
          <section key={group} className="stack stack--3">
            <h4 className="eyebrow">{group}</h4>
            {members
              .filter((setting) => setting.kind === 'toggle')
              .map((setting) => (
                <ToggleSetting key={setting.key} setting={setting} draft={draft} onEdit={edit} />
              ))}
            {fields.length > 0 ? (
              <div className="settings__form">
                {fields.map((setting) => (
                  <FieldSetting key={setting.key} setting={setting} draft={draft} onEdit={edit} />
                ))}
              </div>
            ) : null}
          </section>
        )
      })}

      <div className="setting-row__actions">
        <Button
          variant="primary"
          disabled={!changed || save.isPending}
          onClick={() =>
            save.mutate(serverSettingsBody(settings, draft), {
              onSuccess: (saved) => {
                setDraft({})
                const restart = saved.settings.some((setting) => setting.pending_restart)
                show({
                  title: 'Settings saved',
                  description: restart
                    ? 'Those marked to restart apply when the server next starts.'
                    : 'They apply now.',
                  tone: 'success',
                })
              },
            })
          }
        >
          {save.isPending ? 'Saving…' : 'Save settings'}
        </Button>
        <Button disabled={!changed || save.isPending} onClick={() => setDraft({})}>
          Discard changes
        </Button>
      </div>
    </div>
  )
}

interface SettingProps {
  setting: ServerSetting
  draft: SettingsDraft
  onEdit: (key: string, value: string | null) => void
}

function SourceBadges({ setting }: { setting: ServerSetting }) {
  return (
    <>
      {setting.source === 'environment' ? <Badge>Set by environment</Badge> : null}
      {setting.source === 'database' ? <Badge tone="accent">Saved here</Badge> : null}
      {setting.pending_restart ? (
        <>
          {' '}
          <Badge tone="warning">Restart to apply</Badge>
        </>
      ) : null}
    </>
  )
}

function ResetButton({ setting, draft, onEdit }: SettingProps) {
  if (!canReset(setting, draft)) return null
  return (
    <div className="setting-row__actions">
      <Button variant="ghost" size="sm" onClick={() => onEdit(setting.key, null)}>
        Use the default
      </Button>
    </div>
  )
}

function ToggleSetting({ setting, draft, onEdit }: SettingProps) {
  return (
    <div className="setting-row">
      <div className="setting-row__text">
        <p className="setting-row__label">
          {setting.label} <SourceBadges setting={setting} />
        </p>
        <p className="hint">{settingNote(setting, draft)}</p>
        <ResetButton setting={setting} draft={draft} onEdit={onEdit} />
      </div>
      <Switch
        aria-label={setting.label}
        checked={draftValue(setting, draft) === 'true'}
        disabled={setting.source === 'environment'}
        onCheckedChange={(next) => onEdit(setting.key, String(next))}
      />
    </div>
  )
}

const INPUT_TYPES: Record<string, string> = { number: 'number', time: 'time' }

function FieldSetting({ setting, draft, onEdit }: SettingProps) {
  return (
    <div className="stack stack--1">
      <Field
        label={
          <>
            {setting.label} <SourceBadges setting={setting} />
          </>
        }
        hint={settingNote(setting, draft)}
      >
        <Input
          type={INPUT_TYPES[setting.kind] ?? 'text'}
          numeric={setting.kind === 'number'}
          value={draftValue(setting, draft)}
          disabled={setting.source === 'environment'}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => onEdit(setting.key, event.target.value)}
        />
      </Field>
      <ResetButton setting={setting} draft={draft} onEdit={onEdit} />
    </div>
  )
}
