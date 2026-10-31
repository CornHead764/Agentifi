import { ExternalLink } from 'lucide-react'
import { useState } from 'react'

import { Button, Callout, Dialog, DialogActions, DialogContent, Field, Input } from '@/components/ui'
import { useConnectEbay, useEbayConsent, type MerchantAccount } from '@/lib/clients/merchant'
import { describeApiError } from '@/lib/errors'
import type { MerchantId } from '@/lib/merchants'

/**
 * Connects an eBay account on eBay's own page: the person grants this
 * server's eBay application read access there, and eBay lands them on an
 * address carrying a one-time code, which they paste back here. eBay sends
 * them to its own page rather than back to this server, so a server on a home
 * network needs no address eBay can reach.
 */
export function ConnectEbayDialog({
  merchant,
  account,
  onConnected,
  onClose,
}: {
  merchant: MerchantId
  account: MerchantAccount
  onConnected: (account: MerchantAccount) => void
  onClose: () => void
}) {
  const consent = useEbayConsent(merchant, account.id)
  const connect = useConnectEbay(merchant)
  const [address, setAddress] = useState('')

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent
        title={`Connect ${account.name}`}
        description="Grant this server read access to your eBay purchases on eBay's own page."
        onSubmit={() => {
          if (address.trim() === '') return
          connect.mutate(
            { id: account.id, address },
            {
              onSuccess: (connected) => {
                onConnected(connected)
                onClose()
              },
            },
          )
        }}
        footer={
          <DialogActions onCancel={onClose}>
            <Button
              variant="primary"
              type="submit"
              disabled={address.trim() === '' || connect.isPending || consent.isError}
            >
              Connect
            </Button>
          </DialogActions>
        }
      >
        {consent.isError ? (
          <Callout tone="expense">{describeApiError(consent.error, 'load')}</Callout>
        ) : (
          <>
            <p>
              1. Open eBay, sign in if it asks, and choose <strong>Agree</strong>.{' '}
              {consent.data?.sandbox ? (
                <>This server uses eBay's sandbox: sign in as a sandbox test user.</>
              ) : null}
            </p>
            <p>
              <Button
                variant="secondary"
                size="sm"
                disabled={!consent.data}
                onClick={() => window.open(consent.data?.url, '_blank', 'noopener,noreferrer')}
              >
                <ExternalLink size={14} aria-hidden="true" /> Open eBay
              </Button>
            </p>
            <p>
              2. eBay then shows a page saying access was granted. Copy that page's whole address from
              the browser's address bar and paste it here within five minutes; its code works once.
            </p>
            <Field label="The address eBay showed">
              <Input
                value={address}
                onChange={(event) => setAddress(event.target.value)}
                placeholder="https://auth2.ebay.com/oauth2/ThirdPartyAuthSucessFailure?…"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            {connect.isError ? (
              <Callout tone="expense">{describeApiError(connect.error)}</Callout>
            ) : null}
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
