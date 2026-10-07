/**
 * *Suggest a reminder*, for a billed account that keeps none: the series its
 * bills describe, opened in the series editor with the account already chosen
 * under Bill Connect, so saving creates the series and links it, and the link
 * matches the bank history. When the rows that paid the bills already belong
 * to a reminder, linking that one is offered first: a second series would
 * share their payments with it.
 */

import { useState } from 'react'

import { SeriesEditor } from '@/components/SeriesEditor'
import { Button, Dialog, DialogActions, DialogContent } from '@/components/ui'
import {
  useLinkSeriesToBill,
  useSuggestedReminder,
  type BillSubaccount,
} from '@/lib/clients/bills'
import { useAllSeries } from '@/lib/clients/upcoming'
import { describeApiError, useAccounts, useCategories, useTags } from '@/lib/transactions/queries'

import { suggestionNotice } from './draft'

export function SuggestedReminder({
  subaccount,
  onClose,
}: {
  subaccount: BillSubaccount
  onClose: () => void
}) {
  const suggestion = useSuggestedReminder(subaccount.id)
  const series = useAllSeries('')
  const accounts = useAccounts()
  const categories = useCategories()
  const tags = useTags()
  const link = useLinkSeriesToBill()
  const [anew, setAnew] = useState(false)
  const close = (open: boolean) => {
    if (!open) onClose()
  }

  const found = suggestion.data ?? null
  const paidBy = found?.paid_by_series_id
    ? (series.data ?? []).find((one) => one.id === found.paid_by_series_id)
    : undefined

  if (suggestion.isPending || (found?.paid_by_series_id && series.isPending)) {
    return (
      <Dialog open onOpenChange={close}>
        <DialogContent title="Suggest a reminder">
          <p className="hint">Reading the bills on {subaccount.label}…</p>
        </DialogContent>
      </Dialog>
    )
  }

  if (suggestion.isError) {
    return (
      <Dialog open onOpenChange={close}>
        <DialogContent
          title="Suggest a reminder"
          footer={<DialogActions cancel="Close" onCancel={onClose} />}
        >
          <p className="field__error">{describeApiError(suggestion.error, 'load')}</p>
        </DialogContent>
      </Dialog>
    )
  }

  if (paidBy && !anew) {
    return (
      <Dialog open onOpenChange={close}>
        <DialogContent
          title="Link the reminder these bills already have?"
          footer={
            <DialogActions onCancel={onClose}>
              <Button onClick={() => setAnew(true)}>Suggest a new one</Button>
              <Button
                variant="primary"
                disabled={link.isPending}
                onClick={() =>
                  link.mutate(
                    { seriesId: paidBy.id, subaccountId: subaccount.id },
                    { onSuccess: onClose },
                  )
                }
              >
                Link “{paidBy.label}”
              </Button>
            </DialogActions>
          }
        >
          <p>
            The bank rows that paid these bills already belong to “{paidBy.label}”. Linking it
            keeps it current with each bill; a new reminder would compete with it for the same
            payments.
          </p>
          {link.error ? <p className="field__error">{describeApiError(link.error)}</p> : null}
        </DialogContent>
      </Dialog>
    )
  }

  return (
    <SeriesEditor
      open
      onOpenChange={close}
      suggestion={found}
      billLink={{ connectionId: subaccount.connection_id, subaccountId: subaccount.id }}
      notice={
        found
          ? suggestionNotice(found)
          : `The bills on ${subaccount.label} keep no schedule to read one from, so this starts blank. Saving links it to them.`
      }
      accounts={accounts.data ?? []}
      categories={categories.data ?? []}
      tags={tags.data ?? []}
    />
  )
}
