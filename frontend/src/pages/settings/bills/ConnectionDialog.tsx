import { useState } from 'react'

import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  Input,
} from '@/components/ui'
import { billerById, billerName, connectionTitle, type BillerId } from '@/lib/billers'
import {
  useCreateBillConnection,
  useUpdateBillConnection,
  type AutopayRule,
  type BillConnection,
} from '@/lib/clients/bills'

import { AutopayControls } from './AutopayControls'
import { BillerCatalogue } from './BillerCatalogue'
import { autopayFields } from './draft'
import { SiteField } from './SiteField'

/**
 * Add a login at a provider, or change the one that is there. Credentials are
 * asked for by the sign-in, not here. The provider is chosen once: changing it
 * would leave the subaccounts and bills describing a different company.
 */
export function ConnectionDialog({
  connection,
  preset,
  onClose,
  onCreated,
}: {
  /** The connection being edited, or null to add one. */
  connection: BillConnection | null
  /** A new connection's provider, chosen by whoever opened this; skips the catalogue. */
  preset?: BillerId
  onClose: () => void
  /** The next step after adding — a sign-in, or the mail rule that feeds it. */
  onCreated?: (connection: BillConnection) => void
}) {
  const [biller, setBiller] = useState<BillerId | null>(connection?.biller ?? preset ?? null)
  const [label, setLabel] = useState(connection?.label ?? '')
  const [site, setSite] = useState(connection?.site ?? '')
  const [kind, setKind] = useState<AutopayRule>(connection?.autopay_rule ?? 'none')
  const [figure, setFigure] = useState(
    String(connection?.autopay_days ?? connection?.autopay_day ?? ''),
  )

  const create = useCreateBillConnection()
  const update = useUpdateBillConnection()
  const pending = create.isPending || update.isPending

  const autopay = autopayFields(kind, figure)
  const named = label.trim()
  const chosen = connection?.biller ?? biller
  /** A provider deployed once per customer has no address until the connection names its deployment. */
  const siteField = chosen === null ? undefined : billerById(chosen)?.site
  /** The email-only provider is whatever company the household names, so the name is required. */
  const generic = chosen !== null && billerById(chosen)?.generic === true
  const ready =
    autopay !== null && (connection !== null || biller !== null) && (!generic || named !== '')

  const save = () => {
    if (!ready || autopay === null) return
    const namedSite = siteField === undefined ? '' : site.trim()
    const sited = namedSite === '' ? {} : { site: namedSite }
    if (connection) {
      update.mutate(
        { id: connection.id, patch: { label: named, ...sited, ...autopay } },
        { onSuccess: onClose },
      )
    } else if (biller !== null) {
      create.mutate(
        { biller, label: named, ...sited, ...autopay },
        {
          onSuccess: (created) => {
            onClose()
            onCreated?.(created)
          },
        },
      )
    }
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent
        title={connection ? `Edit ${connectionTitle(connection)}` : 'Add a bill provider'}
        description="One entry per login. Its accounts are listed once signed in."
        onSubmit={(event) => {
          event.preventDefault()
          save()
        }}
        footer={
          <DialogActions>
            <Button type="submit" variant="primary" disabled={!ready || pending}>
              {connection ? 'Save' : 'Add provider'}
            </Button>
          </DialogActions>
        }
      >
        {connection || preset ? null : (
          <BillerCatalogue value={biller} onChange={setBiller} onLeave={onClose} />
        )}

        {generic ? (
          <Field
            label="Company"
            hint="Who sends the bill. A mail rule files its bills here."
          >
            <Input
              value={label}
              onChange={(event) => setLabel(event.target.value)}
              placeholder="Example Water"
              maxLength={80}
            />
          </Field>
        ) : (
          <Field
            label="Name (optional)"
            hint="To tell several logins apart, e.g. Water."
          >
            <Input
              value={label}
              onChange={(event) => setLabel(event.target.value)}
              placeholder={chosen === null ? '' : billerName(chosen)}
              maxLength={80}
            />
          </Field>
        )}

        {siteField === undefined ? null : (
          <SiteField site={siteField} value={site} onChange={setSite} canWait />
        )}

        <AutopayControls
          kind={kind}
          figure={figure}
          invalid={autopay === null}
          onKind={setKind}
          onFigure={setFigure}
        />

      </DialogContent>
    </Dialog>
  )
}
