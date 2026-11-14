/**
 * The count on a navigation link's icon. It is drawn over the icon so a
 * collapsed rail and the phone's tab bar carry it too. The link names the
 * count for a screen reader, so the pill itself is hidden from one.
 */
export function NavCount({ count }: { count: number }) {
  if (count <= 0) return null
  return (
    <span className="nav-count" aria-hidden="true">
      {count > 99 ? '99+' : count}
    </span>
  )
}
