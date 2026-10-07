/** The words and commands the backups card shows, kept apart so a test can read them. */

import { RECIPIENT_KIND_LABELS, type RecipientDescription } from '@/lib/backupKeys'

/** What a set's trigger is called on screen. */
export const TRIGGER_LABELS: Record<string, string> = {
  nightly: 'Nightly',
  manual: 'Back up now',
  upgrade: 'Before an upgrade',
  restore: 'Before a restore',
  'space-delete': 'Before deleting a space',
}

export function triggerLabel(trigger: string): string {
  return TRIGGER_LABELS[trigger] ?? trigger
}

/** The identity file's name, which the restore command reads. */
export const IDENTITY_FILE_NAME = 'agentifi-backup-key.txt'

/**
 * Run on the host, in the directory with docker-compose.yml. The application
 * is stopped first: the restore swaps the database out from under it.
 */
export function restoreCommand(name: string): string {
  return [
    'docker compose stop agentifi',
    `docker compose run --rm -T migrate restore --from ${name} --confirm ${name} --identity - < ${IDENTITY_FILE_NAME}`,
    'docker compose up -d',
  ].join('\n')
}

/** The typed confirmation: the set's name exactly, spaces at either end forgiven. */
export function confirmsRestore(typed: string, name: string): boolean {
  return typed.trim() === name
}

/** A pasted public key the server would refuse. */
export const NOT_A_RECIPIENT =
  'Not a key sets can be encrypted to: an age public key starts age1, an SSH one ssh-ed25519 or ' +
  'ssh-rsa (2048 bits or more).'

/** What a pasted public key was read as, under the field it was pasted into. */
export function recipientSummary(described: RecipientDescription): string {
  const kind = `${RECIPIENT_KIND_LABELS[described.kind]} key`
  return described.comment ? `An ${kind}: ${described.comment}.` : `An ${kind}.`
}

/** `age1abcdefgh…wxyz`, for a list where the whole key is a tooltip away. */
export function shortKey(key: string): string {
  return key.length <= 20 ? key : `${key.slice(0, 12)}…${key.slice(-6)}`
}
