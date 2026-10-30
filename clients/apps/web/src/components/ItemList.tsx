import type { MerchantOrderItem } from '@/lib/clients/merchant'
import { formatDate } from '@/lib/format'
import { itemLabel } from '@/lib/merchants'

/**
 * What was in a purchase, one line per item. A shop with a catalog names the
 * product; the receipt's abbreviation stands until the number was found.
 *
 * `inline` is for a list inside a button, where a `<ul>` is not allowed; the
 * lines carry list roles instead. `limit` shows that many and counts the rest.
 */
export function ItemList({
  items,
  orderedOn,
  showDates = false,
  limit,
  inline = false,
  className,
}: {
  items: MerchantOrderItem[]
  /** The order's date, which a shipment date falls back to when `showDates`. */
  orderedOn?: string
  /** Say when each item shipped — Amazon's orders ship in parts. */
  showDates?: boolean
  limit?: number
  inline?: boolean
  className?: string
}) {
  const List = inline ? 'span' : 'ul'
  const Row = inline ? 'span' : 'li'
  const roles = inline ? { list: { role: 'list' }, item: { role: 'listitem' } } : { list: {}, item: {} }
  const shown = limit && items.length > limit ? items.slice(0, limit) : items
  const more = items.length - shown.length
  return (
    <List className={['merchant-items', className].filter(Boolean).join(' ')} {...roles.list}>
      {shown.map((item, index) => {
        const label = itemLabel(item)
        const href = item.url || item.catalog?.url
        return (
          <Row key={`${item.sku}-${index}`} {...roles.item}>
            {item.quantity > 1 ? `${item.quantity} × ` : ''}
            {href ? (
              <a href={href} target="_blank" rel="noreferrer noopener">
                {label}
              </a>
            ) : (
              label
            )}
            {showDates && item.total_owed !== null ? (
              <span className="muted"> · {formatDate(item.shipped_on ?? orderedOn ?? '', 'short')}</span>
            ) : null}
          </Row>
        )
      })}
      {items.length === 0 ? (
        <Row className="muted" {...roles.item}>
          No items in the file
        </Row>
      ) : null}
      {more > 0 ? (
        <Row className="merchant-items__more muted" {...roles.item}>
          and {more} more
        </Row>
      ) : null}
    </List>
  )
}
