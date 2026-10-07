import { describe, expect, it } from 'vitest'

import type { NewsFeed, NewsItem } from '@/lib/clients/investments'

import { newsSection } from './newsSection'

const STORY: NewsItem = {
  id: 'one',
  title: 'AAA beats expectations',
  publisher: 'Reuters',
  url: 'https://example.test/one',
  thumbnail_url: '',
  symbols: ['AAA'],
  published_at: '2026-08-29T11:00:00Z',
}

function feed(overrides: Partial<NewsFeed> = {}): NewsFeed {
  return {
    items: [STORY],
    available: true,
    unavailable: '',
    symbols: ['AAA'],
    ...overrides,
  }
}

describe('whether the carousel earns its place', () => {
  it('shows the stories it has', () => {
    expect(newsSection(feed())).toBe('shown')
  })

  // An empty carousel reads as "nothing is happening in the market", which is
  // a claim the app is in no position to make.
  it('hides itself before the first read lands', () => {
    expect(newsSection(undefined)).toBe('hidden')
  })

  it('hides itself when the deployment has no news source', () => {
    expect(newsSection(feed({ available: false }))).toBe('hidden')
  })

  it('hides itself when the household holds no securities', () => {
    expect(newsSection(feed({ symbols: [], items: [] }))).toBe('hidden')
  })

  it('hides itself when a working source simply found nothing', () => {
    expect(newsSection(feed({ items: [] }))).toBe('hidden')
  })

  // "Ask again shortly" and "there is no news" are different facts, and the
  // reader is entitled to know which one they are looking at.
  it('stays visible to report a source that would not answer', () => {
    expect(newsSection(feed({ items: [], unavailable: 'the news source is busy' }))).toBe('shown')
  })
})
