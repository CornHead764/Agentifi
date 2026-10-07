import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { NotificationItem, type ShellNotification } from './NotificationItem'
import { LONG_NOTIFICATION } from './notificationText'

// Invented: the shape of a mailbox error passed on whole, one unbroken URL in it.
const longError =
  'The mailbox could not be read: Get "https://mail.example.invalid/v1.0/me/mailFolders/' +
  'inbox/messages?%24filter=receivedDateTime+ge+2026-09-01T00%3A00%3A00Z&%24top=50&%24select=' +
  'id%2Csubject%2Cfrom%2CreceivedDateTime": dial tcp: lookup mail.example.invalid: no such host.'

function render(notification: ShellNotification, onClear?: (id: string) => void): string {
  return renderToStaticMarkup(
    <MemoryRouter>
      <NotificationItem notification={notification} onClear={onClear} />
    </MemoryRouter>,
  )
}

describe('a notification card', () => {
  it('cuts a long body to a few lines and offers the rest', () => {
    expect(longError.length).toBeGreaterThan(LONG_NOTIFICATION)
    const html = render({
      id: 'n1',
      title: 'The bills mailbox stopped reading mail',
      body: longError,
      when: '2 hours',
      href: '/settings/email',
    })
    expect(html).toContain('notifications__text notifications__text--clamped')
    expect(html).toContain('aria-expanded="false"')
    expect(html).toContain('Show more')
    // The toggle is not inside the link, or tapping it would follow the link.
    const link = /<a [^>]*class="notifications__body"[\s\S]*?<\/a>/.exec(html)?.[0] ?? ''
    expect(link).not.toContain('<button')
  })

  it('shows a short body whole, with nothing to expand', () => {
    const html = render({
      id: 'n2',
      title: 'Everyday Checking is down to $40.00',
      body: 'Below the $200.00 you set.',
      when: '4 days',
    })
    expect(html).not.toContain('notifications__text--clamped')
    expect(html).not.toContain('Show more')
  })

  it('offers clearing it, outside the link, when the feed can clear', () => {
    const card = {
      id: 'n3',
      title: 'Everyday Checking is down to $40.00',
      when: '1 day',
      href: '/accounts/everyday',
    }
    const html = render(card, () => undefined)
    expect(html).toContain('aria-label="Clear Everyday Checking is down to $40.00"')
    const link = /<a [^>]*class="notifications__body"[\s\S]*?<\/a>/.exec(html)?.[0] ?? ''
    expect(link).not.toContain('<button')
    expect(render(card)).not.toContain('notifications__clear')
  })
})
