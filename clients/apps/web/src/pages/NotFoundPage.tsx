import { Link, useLocation } from 'react-router-dom'

import { Placeholder } from './Placeholder'

/**
 * The copy names no piece of chrome: the rail is the desktop's navigation and
 * a phone has a tab bar instead, so the way back is a link on the page.
 */
export function NotFoundPage() {
  const { pathname } = useLocation()
  return (
    <Placeholder route={pathname}>
      This page does not exist.{' '}
      <Link to="/" className="placeholder__link">
        Go to the dashboard
      </Link>
      .
    </Placeholder>
  )
}
