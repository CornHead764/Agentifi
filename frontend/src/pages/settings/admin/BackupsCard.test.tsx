import { describe, expect, it } from 'vitest'

import { generateAgeKeyPair } from '@/lib/ageKeys'
import {
  ADMIN_BACKUPS_KEY,
  rehearseRequest,
  withoutRecipient,
  withRecipient,
  type Backups,
  type BackupSet,
} from '@/lib/clients/admin'
import { renderScreen } from '@/test/renderScreen'

import {
  BackupsBody,
  BackupsCard,
  IdentityFields,
  RehearsalReport,
  RestoreHandoff,
  SaveToPasswordManager,
} from './BackupsCard'
import { confirmsRestore, recipientSummary, restoreCommand } from './backupText'

const KEY = 'age1n946z2m9xyhkwem4vn9yj7hj7trelehnwv6q09vqqkzfq4sz3v8qp6rp0m'
// An invented ssh-keygen key and the fingerprint `ssh-keygen -lf` printed for it.
const SSH_KEY = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHtfBk58AasAD2LR2xeQ/qM6+2BhtSPUDwJP25s93+Bw'
const SSH_FINGERPRINT = 'SHA256:zeupREKRB/xQzRoKxYO2h+MbTWn/XoCwc0PDgie88Zk'

function set(overrides: Partial<BackupSet> = {}): BackupSet {
  return {
    name: '2026-03-04_033000_nightly',
    created_at: '2026-03-04T03:30:00Z',
    trigger: 'nightly',
    encrypted: true,
    recipients: [KEY],
    verified: true,
    intact: true,
    problem: null,
    bytes: 2048,
    schema_version: 1,
    parts: [{ part: 'database', bytes: 1024, count: 0 }],
    key_matches: true,
    ...overrides,
  }
}

function backups(overrides: Partial<Backups> = {}): Backups {
  return {
    enabled: true,
    directory: '/backups',
    at: '03:30',
    timezone: 'UTC',
    keep_days: 14,
    recipients: [
      { recipient: KEY, label: 'Safe deposit box', added_at: '2026-03-01T00:00:00Z', kind: 'age', fingerprint: '' },
    ],
    sources: { at: 'database', keep_days: 'environment', recipients: 'database' },
    encrypted: true,
    next_run: '2026-03-05T03:30:00Z',
    running: false,
    problem: null,
    dump_version: '17.6',
    server_version: '17.6',
    runs: [],
    sets: [set()],
    ...overrides,
  }
}

function render(data: Backups) {
  return renderScreen(<BackupsCard />, { seed: [[ADMIN_BACKUPS_KEY, data]] })
}

describe('the backups card', () => {
  it('says loudly that backups are unencrypted until a key is saved', () => {
    const markup = render(backups({ recipients: [], encrypted: false }))
    expect(markup).toContain('Backups are being written unencrypted')
    expect(markup).toContain('Generate a key pair')
  })

  it('stays quiet about encryption once a key is saved', () => {
    const markup = render(backups())
    expect(markup).not.toContain('Backups are being written unencrypted')
    expect(markup).toContain('Safe deposit box')
    expect(markup).toContain('Encrypted')
  })

  it('says backups are off when the server has no directory', () => {
    const markup = render(backups({ enabled: false, directory: '' }))
    expect(markup).toContain('Backups are off')
    expect(markup).toContain('BACKUP_DIR')
  })

  it('shows why a run cannot start, and a failed run in full', () => {
    const markup = renderScreen(
      <BackupsBody
        backups={backups({
          problem: 'pg_dump is version 16 and the server is 17',
          runs: [
            {
              trigger: 'nightly',
              status: 'failed',
              started_at: '2026-03-04T03:30:00Z',
              finished_at: '2026-03-04T03:31:00Z',
              set_name: null,
              encrypted: false,
              bytes: 0,
              error: 'pg_dump: connection refused',
            },
          ],
        })}
      />,
    )
    expect(markup).toContain('pg_dump is version 16 and the server is 17')
    expect(markup).toContain('pg_dump: connection refused')
  })

  it('marks an unencrypted set and one whose connections will not open', () => {
    const markup = render(backups({ sets: [set({ encrypted: false, key_matches: false })] }))
    expect(markup).toContain('Unencrypted')
    expect(markup).toContain('sealed with another key')
  })

  it('names an SSH key by its type and fingerprint', () => {
    const markup = render(
      backups({
        recipients: [
          {
            recipient: SSH_KEY,
            label: 'backup-test@example.invalid',
            added_at: '2026-03-01T00:00:00Z',
            kind: 'ssh-ed25519',
            fingerprint: SSH_FINGERPRINT,
          },
        ],
      }),
    )
    expect(markup).toContain('SSH Ed25519')
    expect(markup).toContain(SSH_FINGERPRINT)
    expect(markup).toContain('backup-test@example.invalid')
  })

  it('says what a pasted public key was read as', () => {
    expect(recipientSummary({ kind: 'ssh-ed25519', comment: 'someone@laptop' })).toBe(
      'An SSH Ed25519 key: someone@laptop.',
    )
    expect(recipientSummary({ kind: 'age', comment: '' })).toBe('An age key.')
  })

  it('names the directory as a bind mount it cannot change', () => {
    const markup = render(backups())
    expect(markup).toContain('/backups')
    expect(markup).toContain('docker-compose.yml')
  })
})

describe('a generated key pair', () => {
  it('sends the server the public key and never the private one', () => {
    const pair = generateAgeKeyPair()
    const body = JSON.stringify(withRecipient(backups(), pair.recipient, 'Laptop'))
    expect(body).toContain(pair.recipient)
    expect(body).toContain(KEY)
    expect(body).not.toContain(pair.identity)
    expect(body).not.toContain('AGE-SECRET-KEY')
  })

  it('offers the private key to a password manager as a sign-up form', () => {
    const markup = renderScreen(<SaveToPasswordManager secret="AGE-SECRET-KEY-1INVENTED" host="192.0.2.10:8100" />)
    expect(markup).toContain('<form')
    expect(markup).toMatch(/<input[^>]*autoComplete="username"[^>]*value="agentifi-backup@192.0.2.10:8100"/)
    expect(markup).toMatch(/<input[^>]*type="password"[^>]*autoComplete="new-password"[^>]*value="AGE-SECRET-KEY-1INVENTED"/)
    expect(markup).toMatch(/<button[^>]*type="submit"[^>]*>.*Save to password manager/)
  })

  it('draws the other ways to keep the key in the same row as the password manager', () => {
    const markup = renderScreen(
      <SaveToPasswordManager secret="AGE-SECRET-KEY-1INVENTED" host="192.0.2.10:8100">
        <button type="button">Download it</button>
      </SaveToPasswordManager>,
    )
    expect(markup).toMatch(
      /<div class="setting-row__actions"><button[^>]*type="submit"[^>]*>.*Save to password manager<\/button><button type="button">Download it<\/button><\/div>/,
    )
  })

  it('removes one key and keeps the rest', () => {
    expect(withoutRecipient(backups(), KEY).recipients).toEqual([])
  })
})

describe('restoring a set', () => {
  const name = '2026-03-04_033000_nightly'

  it('shows the command only once the set name is typed back', () => {
    const hidden = renderScreen(<RestoreHandoff name={name} typed="2026-03-04" onTypedChange={() => {}} />)
    expect(hidden).not.toContain('docker compose run')
    const shown = renderScreen(<RestoreHandoff name={name} typed={name} onTypedChange={() => {}} />)
    expect(shown).toContain(
      `docker compose run --rm -T migrate restore --from ${name} --confirm ${name} --identity - &lt; agentifi-backup-key.txt`,
    )
    expect(shown).toContain('docker compose stop agentifi')
  })

  it('stops the application, restores, then starts it again', () => {
    expect(restoreCommand(name).split('\n')).toEqual([
      'docker compose stop agentifi',
      `docker compose run --rm -T migrate restore --from ${name} --confirm ${name} --identity - < agentifi-backup-key.txt`,
      'docker compose up -d',
    ])
    expect(confirmsRestore(` ${name} `, name)).toBe(true)
    expect(confirmsRestore('latest', name)).toBe(false)
  })

  it('rehearses with the identity that was pasted', () => {
    const request = rehearseRequest({ name, identity: 'AGE-SECRET-KEY-1PASTED', passphrase: '' })
    expect(request.path).toBe(`/admin/backups/sets/${name}/rehearse`)
    expect(request.body).toEqual({ identity: 'AGE-SECRET-KEY-1PASTED', passphrase: '' })
  })

  it('asks for the identity under the login it was saved with, for the password manager to fill', () => {
    const fields = (identity: string) =>
      renderScreen(
        <form>
          <IdentityFields
            host="192.0.2.10:8100"
            identity={identity}
            onIdentityChange={() => {}}
            passphrase=""
            onPassphraseChange={() => {}}
            fileName={null}
            onFileNameChange={() => {}}
          />
        </form>,
      )
    const age = fields('')
    expect(age).toMatch(/<input[^>]*autoComplete="username"[^>]*value="agentifi-backup@192.0.2.10:8100"/)
    expect(age).toMatch(/<input[^>]*type="password"[^>]*autoComplete="current-password"/)
    expect(age).not.toContain('Passphrase')

    const type = 'OPENSSH PRIVATE KEY'
    const ssh = fields(`-----BEGIN ${type}-----\nnot a key\n-----END ${type}-----\n`)
    expect(ssh).toContain('Passphrase')
    expect(ssh).toMatch(/<input[^>]*type="password"[^>]*autoComplete="off"[^>]*name="passphrase"/)
  })

  it('reports the rehearsal’s rows, leaving out the empty tables', () => {
    const markup = renderScreen(
      <RehearsalReport
        report={{
          set: name,
          tables: [
            { table: 'transactions', rows: 12 },
            { table: 'goals', rows: 0 },
          ],
          rows: 12,
          attachments: 3,
          secrets: ['SECRET_KEY'],
          warnings: ['secrets are not restored in place'],
        }}
      />,
    )
    expect(markup).toContain('transactions')
    expect(markup).not.toContain('goals')
    expect(markup).toContain('12 rows')
    expect(markup).toContain('3 attachments')
    expect(markup).toContain('secrets are not restored in place')
  })
})
