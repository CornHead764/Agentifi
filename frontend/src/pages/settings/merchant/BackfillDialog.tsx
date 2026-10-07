import { Button, Dialog, DialogActions, DialogContent } from '@/components/ui'
import {
  useMerchantBackfillWanting,
  useStartMerchantBackfill,
  type MerchantAccount,
} from '@/lib/clients/merchant'
import type { MerchantId } from '@/lib/merchants'

import { backfillWords } from './actions'

/**
 * The confirm before an invoice backfill. A pull files only the orders it
 * reads; this reaches every one on file.
 */
export function BackfillDialog({
  merchant,
  account,
  onStarted,
  onClose,
}: {
  merchant: MerchantId
  account: MerchantAccount
  onStarted: () => void
  onClose: () => void
}) {
  const wanting = useMerchantBackfillWanting(merchant, account.id)
  const start = useStartMerchantBackfill(merchant)
  const count = wanting.data?.wanting
  const words = backfillWords(merchant, account.name, count, wanting.isError)

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent
        title={words.title}
        description={words.how}
        onSubmit={() => {
          if (!count) return
          start.mutate(account.id, {
            onSuccess: () => {
              onStarted()
              onClose()
            },
          })
        }}
        footer={
          <DialogActions onCancel={onClose}>
            <Button variant="primary" type="submit" disabled={!count || start.isPending}>
              {words.submit}
            </Button>
          </DialogActions>
        }
      >
        <p>{words.body}</p>
      </DialogContent>
    </Dialog>
  )
}
