import { ChevronRight } from 'lucide-react'
import { Link } from 'react-router-dom'

/** The chevron every widget header carries through to its own page. */
export function WidgetLink({ to, children }: { to: string; children: string }) {
  return (
    <Link to={to} className="widget__link">
      {children} <ChevronRight size={12} />
    </Link>
  )
}
