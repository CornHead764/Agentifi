import { Link2, Link2Off } from 'lucide-react'

import { Tooltip } from '@/components/ui'
import { billerName } from '@/lib/billers'
import { billLinkMark, type OccurrenceBillLink } from '@/lib/clients/upcoming'

/**
 * The mark beside a reminder a bill provider keeps: a link while the provider
 * answers, a broken link when it does not, nothing for a hand-kept reminder.
 * The tooltip's sentence is also the accessible name.
 */
export function BillLinkMark({ link }: { link: OccurrenceBillLink | null }) {
  const mark = billLinkMark(link, billerName)
  if (!mark) return null
  const Icon = mark.tone === 'linked' ? Link2 : Link2Off
  return (
    <Tooltip label={mark.text} side="top">
      <span
        className={`bill-link-mark bill-link-mark--${mark.tone}`}
        role="img"
        aria-label={mark.text}
        tabIndex={0}
      >
        <Icon size={13} aria-hidden="true" />
      </span>
    </Tooltip>
  )
}
