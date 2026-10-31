import { Download, ExternalLink, Upload } from 'lucide-react'
import { useState } from 'react'

import { Button, Card, CopyButton, Field, FileInput } from '@/components/ui'
import { AccountSelect } from '@/components/AccountSelect'
import {
  useImportMerchantFile,
  type MerchantAccount,
  type MerchantImportResult,
} from '@/lib/clients/merchant'
import { MERCHANTS, type FileSource, type MerchantId } from '@/lib/merchants'
import { saveBlob } from '@/lib/saveFile'
import type { Uuid } from '@/lib/transactions/types'

import { ImportReport } from './ImportReport'

export function ImportCard({
  merchant,
  accounts,
}: {
  merchant: MerchantId
  accounts: MerchantAccount[]
}) {
  const { name, nounPlural, files, fileHint, fileAccept } = MERCHANTS[merchant]
  const [file, setFile] = useState<File | null>(null)
  const [accountId, setAccountId] = useState<Uuid>('')
  const [preview, setPreview] = useState<MerchantImportResult | null>(null)
  const run = useImportMerchantFile(merchant)

  const chosen = accountId || (accounts.length === 1 ? accounts[0].id : '')
  const ready = file !== null && chosen !== ''

  const look = (nextFile: File | null, nextAccount: Uuid) => {
    setPreview(null)
    if (nextFile !== null && nextAccount !== '')
      run.mutate({ file: nextFile, accountId: nextAccount, dryRun: true }, { onSuccess: setPreview })
  }
  const choose = (next: File | null) => {
    setFile(next)
    look(next, chosen)
  }
  const pick = (next: Uuid) => {
    setAccountId(next)
    look(file, next)
  }

  return (
    <Card
      title={`Import ${nounPlural}`}
      subtitle={
        MERCHANTS[merchant].reachDays === null
          ? `A file brings in history older than ${name}'s site shows.`
          : `A file brings in history older than the ${MERCHANTS[merchant].reachDays} days ${name}'s update reaches.`
      }
    >
      <details className="merchant-howto">
        <summary>
          {files.length === 1 ? 'Where the file comes from' : 'Where the files come from'}
        </summary>
        <ol>
          {files.map((source) => (
            <li key={source.title}>
              <Source source={source} />
            </li>
          ))}
        </ol>
      </details>

      <Field label="Whose account">
        <AccountSelect
          accounts={accounts}
          value={chosen}
          onValueChange={pick}
          placeholder={accounts.length === 0 ? 'Add an account first' : 'Choose'}
        />
      </Field>

      <Field label="File" hint={fileHint}>
        <FileInput
          fileName={file?.name ?? null}
          accept={fileAccept}
          onChange={(event) => choose(event.target.files?.[0] ?? null)}
        />
      </Field>

      <div className="row row--wrap merchant-actions">
        <Button
          variant="primary"
          disabled={!ready || run.isPending || preview === null}
          onClick={() =>
            file &&
            run.mutate(
              { file, accountId: chosen, dryRun: false },
              {
                onSuccess: (result) => {
                  setFile(null)
                  setPreview(result)
                },
              },
            )
          }
        >
          <Upload size={14} aria-hidden="true" /> Import
        </Button>
      </div>

      {preview ? <ImportReport merchant={merchant} report={preview} /> : null}
    </Card>
  )
}

/** One file and where it comes from; each source names at most one thing. */
function Source({ source }: { source: FileSource }) {
  return (
    <>
      <strong>{source.title}</strong> {source.body}
      {source.link ? (
        <a href={source.link.href} target="_blank" rel="noreferrer noopener">
          {source.link.text} <ExternalLink size={12} aria-hidden="true" />
        </a>
      ) : null}
      {source.code ? <code>{source.code}</code> : null}
      {source.tail}
      {source.script ? <ScriptButtons script={source.script} /> : null}
    </>
  )
}

/** A script to run in the person's own browser: copied, or saved as a file. */
function ScriptButtons({ script }: { script: NonNullable<FileSource['script']> }) {
  return (
    <span className="row row--wrap merchant-actions">
      <CopyButton size="sm" text={script.text} label="Copy the script" copied="Script copied" />
      <Button
        size="sm"
        variant="ghost"
        onClick={() => saveBlob(new Blob([script.text], { type: 'text/javascript' }), script.name)}
      >
        <Download size={13} aria-hidden="true" />
        Download it
      </Button>
    </span>
  )
}
