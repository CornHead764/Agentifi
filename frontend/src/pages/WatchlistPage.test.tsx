import { describe, expect, it } from 'vitest'

import { renderScreen } from '@/test/renderScreen'
import { WatchlistPage } from './WatchlistPage'

/**
 * The page renders with every query pending, which also reaches
 * `useCreateWatchlist` and `useToast`: both are called on every render, so a
 * hook outside its provider fails here.
 */
function render() {
  return renderScreen(<WatchlistPage />)
}

describe('the watchlist page', () => {
  it('renders with no server behind it', () => {
    expect(render()).toContain('Loading')
  })
})
