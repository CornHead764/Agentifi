import { Badge } from '@/components/ui'
import { describeSource, type MerchantImportResult } from '@/lib/clients/merchant'
import { formatCount, plural } from '@/lib/format'
import { aNoun, MERCHANTS, type MerchantId } from '@/lib/merchants'

export function ImportReport({
  merchant,
  report,
}: {
  merchant: MerchantId
  report: MerchantImportResult
}) {
  const { noun, nounPlural } = MERCHANTS[merchant]
  return (
    <div className="merchant-report">
      <p>
        <Badge tone={report.dry_run ? 'neutral' : 'income'}>
          {report.dry_run ? 'Preview' : 'Imported'}
        </Badge>{' '}
        {describeSource(merchant, report.format)}
        {report.account_hint ? (
          <span className="muted"> · the file says it is {report.account_hint}'s</span>
        ) : null}
      </p>
      <ul>
        <li>
          {plural(report.orders, noun, nounPlural)}, {formatCount(report.new_orders)} not on file
          yet, {plural(report.items, 'item')}
        </li>
        {report.charges > 0 ? (
          <li>
            {plural(report.charges, 'card charge')}
            {report.dry_run ? '' : `, ${report.new_charges} new`}
          </li>
        ) : null}
        {!report.dry_run ? (
          <li>
            {report.matched === 0
              ? `No bank row found its ${noun} this time`
              : `${plural(report.matched, 'bank row')} matched to ${aNoun(merchant)}`}
          </li>
        ) : null}
      </ul>
      {report.warnings.length > 0 ? (
        <details>
          <summary>
            {plural(report.warnings.length, 'line')} skipped
          </summary>
          <ul>
            {report.warnings.slice(0, 20).map((warning) => (
              <li key={warning}>{warning}</li>
            ))}
          </ul>
        </details>
      ) : null}
    </div>
  )
}
