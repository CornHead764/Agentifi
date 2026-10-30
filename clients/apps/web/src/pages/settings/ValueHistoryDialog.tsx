import { useState } from 'react'

import {
  Button,
  DialogActions,
  DialogContent,
  Field,
  FormDialog,
  Textarea,
  useToast,
} from '@/components/ui'
import { useImportValueHistory } from '@/lib/clients/connections'
import { parseValueHistory } from '@/lib/valueHistory'
import { plural } from '@/lib/format'
import type { AccountWithBalances } from '@/lib/transactions/types'

/**
 * Import an account's value history. The server turns stated values into
 * differences against what the account has, so re-importing does not double
 * it and the result counts rows written. Unreadable rows are named.
 */
export function ValueHistoryDialog({
  account,
  open,
  onOpenChange,
}: {
  account: AccountWithBalances
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <FormDialog open={open} onOpenChange={onOpenChange}>
      <ValueHistoryForm account={account} onOpenChange={onOpenChange} />
    </FormDialog>
  )
}

function ValueHistoryForm({
  account,
  onOpenChange,
}: {
  account: AccountWithBalances
  onOpenChange: (open: boolean) => void
}) {
  const { show } = useToast()
  const [text, setText] = useState('')
  const importing = useImportValueHistory()

  const parsed = parseValueHistory(text)

  function send() {
    importing.mutate(
      { accountId: account.id, points: parsed.points },
      {
        onSuccess: ({ written }) => {
          show({
            title:
              written === 0
                ? 'Nothing to add — the ledger already matches this history.'
                : `Added ${plural(written, 'valuation')}.`,
          })
          onOpenChange(false)
        },
      },
    )
  }

  return (
    <DialogContent
      title={`Value history for ${account.name}`}
      description="Two columns: a date and what it was worth that day."
      footer={
        <DialogActions>
          <Button
            variant="primary"
            onClick={send}
            disabled={parsed.points.length === 0 || importing.isPending}
          >
            {importing.isPending
              ? 'Importing…'
              : `Import ${parsed.points.length > 0 ? plural(parsed.points.length, 'row') : ''}`}
          </Button>
        </DialogActions>
      }
    >
      <Field
        label="Paste the export"
        hint="Dates as 2026-01-31. Without a header row, date comes first."
        error={
          parsed.rejected.length > 0
            ? `${plural(parsed.rejected.length, 'line')} could not be read: ${parsed.rejected.join(', ')}`
            : undefined
        }
      >
        <Textarea
          rows={10}
          value={text}
          onChange={(event) => setText(event.target.value)}
          placeholder={'date,value\n2024-01-01,310000.00\n2025-01-01,325000.00'}
        />
      </Field>
    </DialogContent>
  )
}
