/**
 * The Simplifi import, step by step: how to make the export, what a preview
 * says, the write running, and what it left behind.
 */

import { describe, expect, it, vi } from 'vitest'

// Radix portals render nothing under static markup, so the dialog shell is
// replaced with an inline one; everything inside it is the real component.
vi.mock('@/components/ui', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/components/ui')>()),
  ...(await import('@/test/dialogStub')).dialogStub,
}))

import type { ReactNode } from 'react'

import { CURRENT_SPACE_KEY } from '@/lib/clients/spaces'
import type { ImportWarningGroup } from '@/lib/clients/imports'
import type { SimplifiImportPreview, SimplifiImportStatus } from '@/lib/clients/simplifiImport'
import { renderScreen } from '@/test/renderScreen'
import { MigrationNote } from './GetStarted'
import { ImportProblems } from './ImportProblems'
import {
  ExportHowTo,
  ImportDone,
  ImportFailed,
  ImportPreview,
  ImportRunning,
  SimplifiImportDialog,
} from './SimplifiImportDialog'
import { SIMPLIFI_EXPORTER } from './simplifiExporter'

function render(node: ReactNode, isOwner = true): string {
  return renderScreen(node, {
    seed: [[CURRENT_SPACE_KEY, { id: 's1', name: 'Personal', is_owner: isOwner }]],
  })
}

function preview(over: Partial<SimplifiImportPreview> = {}): SimplifiImportPreview {
  return {
    id: 'job-1',
    datasets: null,
    space_name: 'Household',
    counts: { accounts: 3, transactions: 1204, categories: 41, rules: 7, series: 0 },
    rows: 1310,
    errors: [],
    warnings: [],
    refusal: '',
    summary: 'Simplifi import — 0 errors, 0 warnings',
    can_import: true,
    ...over,
  }
}

function status(over: Partial<SimplifiImportStatus> = {}): SimplifiImportStatus {
  return {
    id: 'job-1',
    state: 'running',
    space_name: 'Household',
    started_at: '2026-09-27T10:00:00Z',
    finished_at: null,
    rows: 1310,
    error: '',
    ...over,
  }
}

describe('MigrationNote', () => {
  it('offers the import to an owner and not to anybody else', () => {
    expect(render(<MigrationNote />)).toContain('Import from Simplifi')
    const member = render(<MigrationNote />, false)
    expect(member).not.toContain('Import from Simplifi')
    expect(member).toContain('owner or an admin')
  })

  it('never sends anybody to the server', () => {
    expect(render(<MigrationNote />)).not.toContain('agentifi import')
  })
})

describe('the dialog before a file is chosen', () => {
  it('asks for the export and offers the script, with nothing to import yet', () => {
    const html = render(<SimplifiImportDialog open onOpenChange={() => {}} matchPath="/m" />)
    expect(html).toContain('Simplifi export')
    expect(html).toContain('Transaction rules')
    expect(html).toContain('How to export from Simplifi')
    expect(html).toContain('Preview')
    expect(html).not.toContain('Import 1')
  })
})

describe('ExportHowTo', () => {
  it('hands over the bundled exporter script', () => {
    const html = render(<ExportHowTo />)
    expect(html).toContain('Copy the script')
    expect(html).toContain('Download it')
    expect(html).toContain('transaction-rules')
    // The real script, not a placeholder: the ?raw import reached the file.
    expect(SIMPLIFI_EXPORTER).toContain('Simplifi dataset extractor')
  })
})

describe('ImportPreview', () => {
  it('counts what the export holds and says what importing does', () => {
    const html = render(<ImportPreview preview={preview()} />)
    expect(html).toContain('Household · 1,204 transactions · 3 accounts')
    expect(html).toContain('1,204')
    expect(html).toContain('Rules')
    // A table with nothing in it is not listed.
    expect(html).not.toContain('Bills and income')
    expect(html).toContain('renames this space to Household')
    expect(html).toContain('match them to SimpleFIN')
  })

  it('shows why a space that already holds data is refused', () => {
    const html = render(
      <ImportPreview
        preview={preview({
          id: '',
          can_import: false,
          refusal: 'this space already holds 2 accounts. A Simplifi import fills an empty space',
        })}
      />,
    )
    expect(html).toContain('this space already holds 2 accounts')
    expect(html).toContain('callout--expense')
    expect(html).not.toContain('renames this space')
  })

  it('lists the errors that stop an import, and the warnings that do not', () => {
    const html = render(
      <ImportPreview
        preview={preview({
          id: '',
          can_import: false,
          errors: ['accountsStore a1 subType: unknown value'],
          warnings: [unlinked],
        })}
      />,
    )
    expect(html).toContain('Nothing is imported from this export until these are fixed.')
    expect(html).toContain('accountsStore a1 subType: unknown value')
    expect(html).toContain('accountsStore a2 isConnected: &quot;Savings&quot; was connected')
  })
})

const unlinked: ImportWarningGroup = {
  kind: 'unlinked_accounts',
  note: false,
  summary: 'Accounts arrive unlinked; match each to its SimpleFIN account so it syncs',
  action: 'link_accounts',
  count: 3,
  items: [
    'institutionLoginsStore: 2 Simplifi connections are not carried over',
    'accountsStore a1 isConnected: "Checking" was connected',
    'accountsStore a2 isConnected: "Savings" was connected (x2)',
  ],
}

const notRead: ImportWarningGroup = {
  kind: 'not_read',
  note: true,
  summary: 'Fields and stores this importer does not read, left out',
  action: '',
  count: 7,
  items: ['investmentsQuotesDetailed: not read', 'transactionRulesStore tr1 userModifiedAt: not read (x6)'],
}

describe('ImportProblems', () => {
  it('gives each kind of warning one line with its count, the records folded beneath', () => {
    const html = render(
      <ImportProblems errors={[]} warnings={[unlinked, notRead]} blocked="" written={false} />,
    )
    expect(html.match(/<summary>/g)).toHaveLength(2)
    expect(html).toContain('Accounts arrive unlinked; match each to its SimpleFIN account so it syncs')
    expect(html).toContain('import-warning__count">3<')
    expect(html).toContain('accountsStore a2 isConnected: &quot;Savings&quot; was connected (x2)')
    expect(html).toContain('Fields and stores this importer does not read, left out')
    expect(html).toContain('userModifiedAt: not read (x6)')
    expect(html).toContain('import-warnings--notes')
  })

  it('links to where a warning is resolved only once the import is written', () => {
    const before = render(
      <ImportProblems errors={[]} warnings={[unlinked]} blocked="" written={false} />,
    )
    expect(before).not.toContain('Match accounts')
    expect(before).toContain('Once the import is done')

    const after = render(<ImportProblems errors={[]} warnings={[unlinked]} blocked="" written />)
    expect(after).toContain('Match accounts')
    expect(after).toContain('href="/settings/accounts#connections"')
    expect(after).not.toContain('Once the import is done')
  })

  it('offers no link for a note or for a kind with no place to resolve it', () => {
    const html = render(<ImportProblems errors={[]} warnings={[notRead]} blocked="" written />)
    expect(html).not.toContain('href=')
  })
})

describe('the write', () => {
  it('reports elapsed time, not a made-up percentage', () => {
    const html = render(
      <ImportRunning status={status()} now={Date.parse('2026-09-27T10:00:12Z')} />,
    )
    expect(html).toContain('Importing 1,310 rows')
    expect(html).toContain('12 seconds so far')
    expect(html).not.toContain('%')
    expect(html).toContain('spinner')
  })

  it('says where to go next once it is done', () => {
    const html = render(
      <ImportDone status={status({ state: 'done', finished_at: '2026-09-27T10:00:14Z' })} />,
    )
    expect(html).toContain('1,310 rows imported. This space is now Household.')
    expect(html).toContain('match each imported account to its SimpleFIN account')
  })

  it('says a failed write left nothing behind, and why', () => {
    const html = render(
      <ImportFailed status={status({ state: 'failed', error: 'this space already holds 1 tags' })} />,
    )
    expect(html).toContain('Nothing was written.')
    expect(html).toContain('this space already holds 1 tags')
    expect(html).toContain('Try again reads the same files')
  })

  it('keeps what the preview warned of on the finished screen, with links to act on it', () => {
    const html = render(
      <ImportDone status={status({ state: 'done' })}>
        <ImportProblems errors={[]} warnings={[unlinked]} blocked="" written />
      </ImportDone>,
    )
    expect(html).toContain('Accounts arrive unlinked')
    expect(html).toContain('Match accounts')
  })
})
