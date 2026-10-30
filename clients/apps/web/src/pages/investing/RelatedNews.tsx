import { Newspaper } from 'lucide-react'
import { useState } from 'react'

import { Badge, Card } from '@/components/ui'
import { useInvestmentNews, type NewsItem } from '@/lib/clients/investments'
import { timeAgo } from '@/lib/format'

import { newsSection } from './newsSection'

/**
 * The stories behind the tickers this household holds. Removes itself with no
 * news source or no securities held (see `newsSection`); a source that would
 * not answer gets the card, with the reason.
 */
export function RelatedNews() {
  const news = useInvestmentNews()
  const feed = news.data
  if (newsSection(feed) === 'hidden') return null
  // Narrowing for TypeScript: `hidden` is the only verdict for an absent feed.
  if (!feed) return null

  return (
    <Card title="Related news" subtitle="Keyed to the tickers you hold.">
      {feed.unavailable ? (
        <p className="news__unavailable">{feed.unavailable}</p>
      ) : (
        // Horizontal scroll rather than a paged carousel: a finance screen
        // should not hide a story behind a control the reader has to discover.
        <ul className="news__rail">
          {feed.items.map((item) => (
            <NewsCard key={item.id} item={item} />
          ))}
        </ul>
      )}
    </Card>
  )
}

function NewsCard({ item }: { item: NewsItem }) {
  // A thumbnail that will not load falls back to the icon, as does a story
  // with no image.
  const [imageFailed, setImageFailed] = useState(false)
  const showImage = item.thumbnail_url !== '' && !imageFailed

  return (
    <li className="news__card">
      {/* noreferrer as well as noopener: the referrer would name the app the
          household reads its finances in. */}
      <a href={item.url} target="_blank" rel="noreferrer noopener" className="news__link">
        <span className="news__thumb">
          {showImage ? (
            // no-referrer: the default referrer would tell the news source's CDN
            // which page of a personal finance app the reader is on.
            <img
              src={item.thumbnail_url}
              alt=""
              loading="lazy"
              referrerPolicy="no-referrer"
              onError={() => setImageFailed(true)}
            />
          ) : (
            <Newspaper size={18} aria-hidden="true" />
          )}
        </span>
        <span className="stack stack--2 news__body">
          <span className="news__title">{item.title}</span>
          <span className="news__meta">
            {item.symbols.map((symbol) => (
              <Badge key={symbol} tone="accent">
                {symbol}
              </Badge>
            ))}
            <span className="news__source">
              {[item.publisher, timeAgo(item.published_at)].filter(Boolean).join(' · ')}
            </span>
          </span>
        </span>
      </a>
    </li>
  )
}
