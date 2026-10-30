import { FileUp, RotateCcw, Upload } from 'lucide-react'
import { useState } from 'react'

import { Facts } from '@/components/Facts'
import { MigrationNote } from '@/components/onboarding/GetStarted'
import { ImportProblems } from '@/components/onboarding/ImportProblems'
import { Button, Callout, Card, Field, FileInput, Input, Spinner } from '@/components/ui'
import { ApiError } from '@/lib/api'
import {
  useImportFile,
  type ImportResult,
} from '@/lib/clients/imports'
import { describeApiError } from '@/lib/errors'
import { plural } from '@/lib/format'

/**
 * Importing a bank statement. Preview first, always: a wrong account or a
 * wrong file is visible in the report and invisible afterwards. Re-importing
 * is safe, since rows carry the bank's own ids.
 */
export function ImportFileCard() {
  const [file, setFile] = useState<File | null>(null)
  const [account, setAccount] = useState('')
  const [preview, setPreview] = useState<ImportResult | null>(null)
  const [failure, setFailure] = useState<string | null>(null)

  const run = useImportFile()

  const choose = (chosen: File | null) => {
    setFile(chosen)
    // The old report describes the old file. Leaving it on screen beside a new
    // one is how somebody imports a statement having read about another.
    setPreview(null)
    setFailure(null)
  }

  const reset = () => {
    // `FileInput` empties its own element when the name goes away.
    choose(null)
    setAccount('')
  }

  // A file the server cannot represent answers 422 with the report itself.
  const fail = (error: unknown) => {
    if (error instanceof ApiError && error.status === 422 && isReport(error.body)) {
      setPreview(error.body)
      return
    }
    setFailure(describeApiError(error))
  }

  const stuck = failure !== null || (preview !== null && preview.errors.length > 0)

  return (
    <Card
      id="import"
      title="Import a statement"
      subtitle="OFX, QFX or a Simplifi CSV. You see a preview before anything is written."
    >
      <Field label="File" hint="Format is read from the contents, not the name.">
        <FileInput
          fileName={file?.name ?? null}
          accept=".ofx,.qfx,.csv,text/csv,application/x-ofx,text/plain"
          onChange={(event) => choose(event.target.files?.[0] ?? null)}
        />
      </Field>

      <Field
        label="File these under"
        hint="OFX only. Blank uses the account the file names."
      >
        <Input
          value={account}
          placeholder="Everyday Checking"
          onChange={(event) => setAccount(event.target.value)}
        />
      </Field>

      <div className="import__actions">
        <Button
          disabled={file === null || run.isPending}
          onClick={() => {
            if (!file) return
            setPreview(null)
            setFailure(null)
            run.mutate({ file, account, dryRun: true }, { onSuccess: setPreview, onError: fail })
          }}
        >
          {run.isPending && preview === null ? (
            <Spinner size={13} />
          ) : (
            <FileUp size={13} aria-hidden="true" />
          )}
          {run.isPending && preview === null ? 'Reading…' : 'Preview'}
        </Button>

        {preview && preview.errors.length === 0 ? (
          <Button
            variant="primary"
            disabled={run.isPending}
            onClick={() => {
              if (!file) return
              run.mutate({ file, account, dryRun: false }, { onSuccess: () => reset(), onError: fail })
            }}
          >
            {run.isPending ? <Spinner size={13} /> : <Upload size={13} aria-hidden="true" />}
            {run.isPending ? 'Importing…' : `Import ${preview.transactions} transactions`}
          </Button>
        ) : null}

        {stuck ? (
          <Button onClick={reset}>
            <RotateCcw size={13} aria-hidden="true" />
            Start over
          </Button>
        ) : null}
      </div>

      {failure ? <Callout tone="expense">{failure}</Callout> : null}
      {stuck ? (
        <p className="hint">
          Fix the file and choose it again, or press Preview to read the same file again.
        </p>
      ) : null}

      {run.data && !run.data.dry_run && run.data.written ? (
        <>
          <Facts
            facts={[
              { label: 'Imported', value: plural(run.data.written.transactions, 'transaction') },
              run.data.written.transactions_skipped > 0 && {
                label: 'Already here',
                value: plural(run.data.written.transactions_skipped, 'transaction'),
              },
              run.data.written.accounts > 0 && {
                label: 'Accounts created',
                value: run.data.written.accounts,
              },
            ]}
          />
          <ImportProblems
            errors={[]}
            warnings={run.data.warnings ?? []}
            blocked=""
            written
          />
        </>
      ) : null}

      {preview ? <ImportPreview preview={preview} /> : null}

      <MigrationNote />
    </Card>
  )
}

function isReport(body: unknown): body is ImportResult {
  return typeof body === 'object' && body !== null && 'errors' in body && 'summary' in body
}

function ImportPreview({ preview }: { preview: ImportResult }) {
  return (
    <div className="import__preview">
      <p className="import__headline">
        {preview.format.toUpperCase()} · {preview.transactions} transactions ·{' '}
        {preview.accounts.length === 0
          ? 'no account named'
          : preview.accounts.join(', ')}
      </p>

      <ImportProblems
        errors={preview.errors ?? []}
        warnings={preview.warnings ?? []}
        blocked="Nothing is imported from this file until these are fixed."
        written={false}
      />

      {/* The command line's own report, verbatim. */}
      <pre className="import__report">{preview.summary}</pre>
    </div>
  )
}
