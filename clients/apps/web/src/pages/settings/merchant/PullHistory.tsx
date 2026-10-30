import { useState } from 'react'

import { Button, Dialog, DialogActions, DialogContent, Field, Input } from '@/components/ui'
import { daysBackTo, type MerchantAccount } from '@/lib/clients/merchant'
import { toIsoDate } from '@/lib/format'
import { MERCHANTS, type MerchantId } from '@/lib/merchants'

/**
 * The pull, reaching back from a date in one run. The agent reads a capped
 * number of invoices per run and says when more wait; running it again picks
 * up the rest.
 */
export function PullHistory({
  merchant,
  account,
  busy,
  onPull,
  onClose,
}: {
  merchant: MerchantId
  account: MerchantAccount
  busy: boolean
  onPull: (days: number, backTo: string) => void
  onClose: () => void
}) {
  const { nounPlural } = MERCHANTS[merchant]
  const [backTo, setBackTo] = useState('')
  const days = daysBackTo(backTo)
  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent
        title={`Fetch ${account.name}'s history`}
        description={`Reads ${nounPlural} back to this date so older rows can match. Up to 150 invoices per run; it says when more remain.`}
        onSubmit={() => {
          if (days === null) return
          onPull(days, backTo)
          onClose()
        }}
        footer={
          <DialogActions onCancel={onClose}>
            <Button variant="primary" type="submit" disabled={days === null || busy}>
              {days === null ? 'Fetch' : `Fetch ${days} days`}
            </Button>
          </DialogActions>
        }
      >
        <Field label="Back to" hint="Up to ten years.">
          <Input
            type="date"
            value={backTo}
            onChange={(event) => setBackTo(event.target.value)}
            max={toIsoDate(new Date())}
          />
        </Field>
      </DialogContent>
    </Dialog>
  )
}
