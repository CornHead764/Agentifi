import { Download, FileUp, RotateCcw, Upload } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { Facts } from '@/components/Facts'
import {
  Button,
  Callout,
  CopyButton,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  FileInput,
  OptionSelect,
  Spinner,
} from '@/components/ui'
import {
  useInvalidateAfterImport,
  useSimplifiImportPreview,
  useSimplifiImportStatus,
  useStartSimplifiImport,
  type SimplifiImportPreview,
  type SimplifiImportStatus,
} from '@/lib/clients/simplifiImport'
import { describeApiError } from '@/lib/errors'
import { formatCount, plural } from '@/lib/format'
import { saveBlob } from '@/lib/saveFile'

import { ImportProblems } from './ImportProblems'
import { SIMPLIFI_EXPORTER, SIMPLIFI_EXPORTER_NAME } from './simplifiExporter'

/**
 * Filling this space from a Simplifi export: choose the file, read what it
 * holds and what would stop it, import, and watch the write finish. Nothing is
 * written before Import is pressed, and the server refuses a space that
 * already holds data.
 *
 * Closing the dialog forgets everything but a write still running, so it
 * opens on a clean start; after any failure the footer offers the way back.
 */
export function SimplifiImportDialog({
  open,
  onOpenChange,
  matchPath,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Where imported accounts are matched to SimpleFIN. */
  matchPath: string
}) {
  const [exportFile, setExportFile] = useState<File | null>(null)
  const [rulesFile, setRulesFile] = useState<File | null>(null)
  const [dataset, setDataset] = useState('')
  const [preview, setPreview] = useState<SimplifiImportPreview | null>(null)
  const [jobId, setJobId] = useState<string | null>(null)
  const [failure, setFailure] = useState<string | null>(null)

  const fail = (error: unknown) => setFailure(describeApiError(error))
  const read = useSimplifiImportPreview()
  const start = useStartSimplifiImport()
  const status = useSimplifiImportStatus(jobId)
  const invalidate = useInvalidateAfterImport()
  const state = status.data?.state

  useEffect(() => {
    if (state === 'done') void invalidate()
  }, [state, invalidate])

  // A report about the previous file must not sit beside a new one.
  const forget = () => {
    setPreview(null)
    setFailure(null)
  }
  const chooseAgain = () => {
    forget()
    setJobId(null)
    setExportFile(null)
    setRulesFile(null)
    setDataset('')
    read.reset()
    start.reset()
  }
  const readFiles = () => {
    if (!exportFile) return
    forget()
    read.mutate(
      { exportFile, rulesFile, dataset },
      { onSuccess: (result) => setPreview(result), onError: fail },
    )
  }
  const tryAgain = () => {
    setJobId(null)
    start.reset()
    readFiles()
  }

  const running = jobId !== null && (state === undefined || state === 'running')
  const close = (next: boolean) => {
    if (!next && !running) chooseAgain()
    onOpenChange(next)
  }
  const title = state === 'done' ? 'Imported from Simplifi' : 'Import from Simplifi'
  const warnings = preview?.warnings ?? []

  let body
  let footer
  if (jobId !== null && status.data && state === 'done') {
    body = (
      <ImportDone status={status.data}>
        <ImportProblems
          errors={[]}
          warnings={warnings}
          blocked=""
          written
          onNavigate={() => close(false)}
        />
      </ImportDone>
    )
    footer = (
      <DialogActions cancel="Close">
        <Button asChild variant="primary">
          <Link to={matchPath} onClick={() => close(false)}>
            Match accounts
          </Link>
        </Button>
      </DialogActions>
    )
  } else if (jobId !== null && status.data && state === 'failed') {
    body = <ImportFailed status={status.data} />
    footer = (
      <DialogActions
        start={
          <Button onClick={chooseAgain}>
            <FileUp size={13} aria-hidden="true" />
            Choose another file
          </Button>
        }
      >
        <Button variant="primary" onClick={tryAgain}>
          <RotateCcw size={13} aria-hidden="true" />
          Try again
        </Button>
      </DialogActions>
    )
  } else if (running) {
    body = <ImportRunning status={status.data ?? null} />
    footer = <DialogActions cancel="Close" />
  } else {
    const stuck = failure !== null || (preview !== null && !preview.can_import && !preview.datasets)
    body = (
      <>
        <ExportHowTo />
        <Field label="Simplifi export" hint="The simplifi-export-….json file the script saved.">
          <FileInput
            fileName={exportFile?.name ?? null}
            accept=".json,application/json"
            onChange={(event) => {
              setExportFile(event.target.files?.[0] ?? null)
              setDataset('')
              forget()
            }}
          />
        </Field>
        <Field label="Transaction rules" hint="Optional. transaction-rules.json, saved from the Network panel as step 3 describes.">
          <FileInput
            fileName={rulesFile?.name ?? null}
            accept=".json,application/json"
            onChange={(event) => {
              setRulesFile(event.target.files?.[0] ?? null)
              forget()
            }}
          />
        </Field>
        {preview?.datasets && preview.datasets.length > 1 ? (
          <Field label="Dataset">
            <OptionSelect
              value={dataset}
              onValueChange={(chosen) => {
                setDataset(chosen)
                setPreview(null)
              }}
              placeholder="Choose"
              options={preview.datasets.map((id) => ({ value: id, label: id }))}
            />
          </Field>
        ) : null}
        {failure ? <Callout tone="expense">{failure}</Callout> : null}
        {preview ? <ImportPreview preview={preview} /> : null}
        {stuck ? (
          <p className="hint">
            Fix the file and choose it again, or press Preview to read the same file again.
          </p>
        ) : null}
      </>
    )
    footer = (
      <DialogActions
        start={
          stuck ? (
            <Button onClick={chooseAgain}>
              <RotateCcw size={13} aria-hidden="true" />
              Start over
            </Button>
          ) : (
            <Button disabled={exportFile === null || read.isPending || start.isPending} onClick={readFiles}>
              {read.isPending ? <Spinner size={13} /> : <FileUp size={13} aria-hidden="true" />}
              {read.isPending ? 'Reading…' : 'Preview'}
            </Button>
          )
        }
      >
        {stuck ? (
          <Button disabled={exportFile === null || read.isPending} onClick={readFiles}>
            <FileUp size={13} aria-hidden="true" />
            Preview
          </Button>
        ) : null}
        {preview?.can_import ? (
          <Button
            variant="primary"
            disabled={read.isPending || start.isPending}
            onClick={() =>
              start.mutate(preview.id, {
                onSuccess: (started) => setJobId(started.id),
                onError: fail,
              })
            }
          >
            {start.isPending ? <Spinner size={13} /> : <Upload size={13} aria-hidden="true" />}
            {start.isPending ? 'Starting…' : `Import ${plural(preview.rows, 'row')}`}
          </Button>
        ) : null}
      </DialogActions>
    )
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent wide title={title} footer={footer} className="simplifi-import">
        <div className="simplifi-import__body">{body}</div>
      </DialogContent>
    </Dialog>
  )
}

/** How to get the export out of Simplifi, with the script to paste. */
export function ExportHowTo() {
  return (
    <details className="merchant-howto">
      <summary>How to export from Simplifi</summary>
      <ol>
        <li>
          Sign in to simplifi.quicken.com in Chrome and open Transactions, Net Worth, Spending
          Plan, Investments and Bills &amp; Income once each, so every page has loaded its data.
        </li>
        <li>
          Open the browser console (F12, then Console), paste the exporter script and press
          Enter. It only reads, and it saves one .json file.
          <span className="simplifi-import__script">
            <CopyButton
              size="sm"
              text={SIMPLIFI_EXPORTER}
              label="Copy the script"
              copied="Script copied"
            />
            <Button size="sm" variant="ghost" onClick={downloadExporter}>
              <Download size={13} aria-hidden="true" />
              Download it
            </Button>
          </span>
        </li>
        <li>
          Optional: your transaction rules are not in that file, and Simplifi only asks for them
          when its rules page opens. With the developer tools still open, switch to the Network
          panel and type <code>transaction-rules</code> in its filter box. Then, in Simplifi, open
          Settings, then Rules (reload the page if you are already there). Right-click the{' '}
          <code>transaction-rules</code> request that appears, choose Copy, then Copy response,
          and paste it into a file named transaction-rules.json.
        </li>
        <li>Simplifi&rsquo;s export needs a live subscription. Take it before yours ends.</li>
      </ol>
    </details>
  )
}

function downloadExporter() {
  saveBlob(new Blob([SIMPLIFI_EXPORTER], { type: 'text/javascript' }), SIMPLIFI_EXPORTER_NAME)
}

/** The rows a person recognises, in the order Simplifi shows them. */
const COUNTED: [string, string, string][] = [
  ['accounts', 'Accounts', 'account'],
  ['transactions', 'Transactions', 'transaction'],
  ['categories', 'Categories', 'category'],
  ['tags', 'Tags', 'tag'],
  ['rules', 'Rules', 'rule'],
  ['series', 'Bills and income', 'series'],
  ['goals', 'Savings goals', 'goal'],
  ['watchlists', 'Watchlists', 'watchlist'],
  ['spending_plan_months', 'Spending plan months', 'month'],
  ['holdings', 'Holdings', 'holding'],
]

export function ImportPreview({ preview }: { preview: SimplifiImportPreview }) {
  const counts = preview.counts ?? {}
  return (
    <div className="import__preview">
      {preview.space_name ? (
        <p className="import__headline">
          {preview.space_name} · {plural(counts.transactions ?? 0, 'transaction')} ·{' '}
          {plural(counts.accounts ?? 0, 'account')}
        </p>
      ) : null}

      {preview.refusal ? <Callout tone="expense">{preview.refusal}</Callout> : null}

      {preview.counts ? (
        <Facts
          facts={COUNTED.map(
            ([table, label]) =>
              (counts[table] ?? 0) > 0 && { label, value: formatCount(counts[table]) },
          )}
        />
      ) : null}

      <ImportProblems
        errors={preview.errors ?? []}
        warnings={preview.warnings ?? []}
        blocked="Nothing is imported from this export until these are fixed."
        written={false}
      />

      {preview.can_import ? (
        <p className="hint">
          Importing renames this space to {preview.space_name}. Accounts arrive unlinked; you match
          them to SimpleFIN afterwards.
        </p>
      ) : null}

      {preview.summary ? (
        <details>
          <summary>Full report</summary>
          {/* The command line's own report, verbatim. */}
          <pre className="import__report">{preview.summary}</pre>
        </details>
      ) : null}
    </div>
  )
}

/** The write is one transaction: it is running or it is finished, so no percentage. */
export function ImportRunning({
  status,
  now,
}: {
  status: SimplifiImportStatus | null
  now?: number
}) {
  const [ticked, setTicked] = useState(() => Date.now())
  useEffect(() => {
    const timer = window.setInterval(() => setTicked(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])
  const started = status?.started_at ? Date.parse(status.started_at) : null
  const seconds = started === null ? 0 : Math.max(0, Math.round(((now ?? ticked) - started) / 1000))
  return (
    <div className="import__preview" role="status">
      <p className="import__headline import__reading">
        <Spinner size={16} />
        Importing {status ? plural(status.rows, 'row') : ''}…
      </p>
      <p className="hint">
        {seconds > 0 ? `${plural(seconds, 'second')} so far. ` : ''}
        Usually under a minute. It carries on if you close this.
      </p>
    </div>
  )
}

export function ImportDone({
  status,
  children,
}: {
  status: SimplifiImportStatus
  /** What the preview warned of, to act on once it is written. */
  children?: ReactNode
}) {
  return (
    <div className="import__preview">
      <p className="import__headline">
        {plural(status.rows, 'row')} imported. This space is now {status.space_name}.
      </p>
      <p>
        Next, match each imported account to its SimpleFIN account, so syncing carries on from
        where Simplifi stopped. Until then the accounts are unlinked and do not sync.
      </p>
      {children}
    </div>
  )
}

export function ImportFailed({ status }: { status: SimplifiImportStatus }) {
  return (
    <div className="import__preview">
      <p className="import__headline">The import did not finish. Nothing was written.</p>
      {status.error ? <Callout tone="expense">{status.error}</Callout> : null}
      <p className="hint">
        Try again reads the same files and imports them afresh. If a file was the problem, choose
        a corrected one.
      </p>
    </div>
  )
}
