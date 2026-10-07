import type { ServerSetting, SettingsDraft } from '@/lib/clients/admin'

/** The settings in the server's order, under their groups. */
export function groupSettings(settings: ServerSetting[]): [string, ServerSetting[]][] {
  const groups = new Map<string, ServerSetting[]>()
  for (const setting of settings) {
    const members = groups.get(setting.group) ?? []
    members.push(setting)
    groups.set(setting.group, members)
  }
  return [...groups]
}

/** A setting's help, then where its value comes from and when it applies. */
export function settingNote(setting: ServerSetting, draft: SettingsDraft): string {
  const parts = setting.help ? [setting.help] : []
  if (setting.source === 'environment') {
    parts.push(`Set by ${setting.key} in the environment or .env, which wins over this screen; change it there.`)
    return parts.join(' ')
  }
  const shown = setting.default === '' ? 'empty' : setting.default
  if (draft[setting.key] === null) parts.push('Goes back to the default when saved.')
  else if (setting.source === 'database') parts.push(`Saved here; the default is ${shown}.`)
  else parts.push(`The default, until saved here or set as ${setting.key}.`)
  if (!setting.live) parts.push('Applies when the server next starts.')
  return parts.join(' ')
}

/** Whether "Use the default" has anything to undo. */
export function canReset(setting: ServerSetting, draft: SettingsDraft): boolean {
  if (setting.source === 'environment') return false
  const edited = draft[setting.key]
  if (edited === null) return false
  return setting.source === 'database' || edited !== undefined
}

/**
 * Twelve characters of the commit, as `git log --oneline` prints it, keeping
 * a suffix such as `-dirty` that a local build adds.
 */
export function shortCommit(commit: string): string {
  return commit.replace(/^([0-9a-f]{12})[0-9a-f]*/, '$1')
}
