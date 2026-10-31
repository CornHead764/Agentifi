import { RefreshCw } from 'lucide-react'

import { useMoneyText } from '@/components/moneyText'
import { Button, Card, Spinner, useProgressToast } from '@/components/ui'
import { useRepricing, useRevalueAllAssets, useValuationSources } from '@/lib/clients/connections'
import { accountTypeLabel } from '@/lib/accountTypes'
import { repricingTitle, revaluationFailureTitle, revaluationReport } from '@/lib/revaluation'

/**
 * What prices property and vehicle accounts, and a way to re-price them now.
 * The lookups run in the server's Camoufox browser, so there is nothing to
 * set here.
 */
export function AssetValuationCard() {
  const progress = useProgressToast()
  const moneyText = useMoneyText()
  const sources = useValuationSources()
  const revalueAll = useRevalueAllAssets()
  const pending = useRepricing()

  const configured = sources.data?.configured ?? []
  if (!sources.isSuccess) return null

  return (
    <Card
      title="Asset valuation"
      subtitle="Optional estimates from Zillow or Kelley Blue Book, read through the Camoufox browser."
    >
      <div className="setting-row">
        <div className="setting-row__text">
          <p className="hint">
            {configured.length > 0
              ? `Pricing ${configured.map((type) => accountTypeLabel(type)).join(' and ')} is live.`
              : 'Available once the server admin configures the Camoufox browser.'}
          </p>
        </div>
        {configured.length > 0 ? (
          <div className="setting-row__actions">
            <Button
              size="sm"
              disabled={pending}
              onClick={() =>
                void progress(
                  repricingTitle(),
                  revalueAll.mutateAsync(),
                  ({ results }) => revaluationReport(results, moneyText),
                  revaluationFailureTitle(),
                )
              }
            >
              {pending ? (
                <Spinner size={13} />
              ) : (
                <RefreshCw size={13} aria-hidden="true" />
              )}
              {pending ? 'Re-pricing…' : 'Re-price all now'}
            </Button>
          </div>
        ) : null}
      </div>
    </Card>
  )
}
