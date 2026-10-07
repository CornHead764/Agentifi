import type { NewsFeed } from '@/lib/clients/investments'

/** What the related-news section should do with a feed. */
export type NewsSection = 'hidden' | 'shown'

/**
 * Whether the carousel is shown. Hidden with no news source or no securities
 * held, where an empty carousel would read as a quiet market. A source that
 * would not answer is shown, since "ask again shortly" is a different fact
 * from "there is no news".
 */
export function newsSection(feed: NewsFeed | undefined): NewsSection {
  if (!feed) return 'hidden'
  if (!feed.available) return 'hidden'
  if ((feed.symbols ?? []).length === 0) return 'hidden'
  if (feed.unavailable) return 'shown'
  return (feed.items ?? []).length > 0 ? 'shown' : 'hidden'
}
