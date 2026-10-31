import { X } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { IconButton } from '@/components/ui'

import { isLongNotification } from './notificationText'

/** What the shell needs to draw an alert card. */
export interface ShellNotification {
  id: string
  title: string
  body?: string
  /** Already humanised — "2 hours", "4 days". */
  when: string
  unread?: boolean
  /** Where the alert is about, if there is somewhere to go. */
  href?: string
}

export interface NotificationItemProps {
  notification: ShellNotification
  /** Called when the card is opened, so it can be marked read. */
  onOpen?: (id: string) => void
  /** Takes the card off the feed. */
  onClear?: (id: string) => void
}

export function NotificationItem({ notification, onOpen, onClear }: NotificationItemProps) {
  const [expanded, setExpanded] = useState(false)
  const long = isLongNotification(notification.body)
  const content = (
    <>
      <p className="notifications__title">{notification.title}</p>
      {notification.body ? (
        <p
          className={
            long && !expanded
              ? 'hint notifications__text notifications__text--clamped'
              : 'hint notifications__text'
          }
        >
          {notification.body}
        </p>
      ) : null}
    </>
  )

  return (
    <li className="notifications__item">
      <div className="notifications__content">
        {/* Opening a card is what marks it read: a badge that only cleared
            from the header would make somebody choose between losing the
            count and losing the list. */}
        {notification.href ? (
          <Link
            to={notification.href}
            className="notifications__body"
            onClick={() => onOpen?.(notification.id)}
          >
            {content}
          </Link>
        ) : (
          <div className="notifications__body">{content}</div>
        )}
        {/* Beside the link rather than in it: a button inside an anchor is
            two controls in one, and the tap would follow the link. */}
        {long ? (
          <button
            type="button"
            className="notifications__more"
            aria-expanded={expanded}
            onClick={() => setExpanded((open) => !open)}
          >
            {expanded ? 'Show less' : 'Show more'}
          </button>
        ) : null}
      </div>
      <div className="notifications__meta">
        {notification.unread ? (
          <span className="notifications__unread" aria-label="Unread" />
        ) : null}
        {notification.when}
        {onClear ? (
          <IconButton
            label={`Clear ${notification.title}`}
            variant="ghost"
            size="sm"
            className="notifications__clear"
            onClick={() => onClear(notification.id)}
          >
            <X size={14} />
          </IconButton>
        ) : null}
      </div>
    </li>
  )
}
