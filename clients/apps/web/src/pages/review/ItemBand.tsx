import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

import { Money } from '@/components/Money'
import { Button, ConfirmDialog, Input } from '@/components/ui'
import {
  useReviewAct,
  type ReviewActionOption,
  type ReviewItem,
} from '@/lib/clients/review'
import { plural, relativeDay } from '@agentifi/core/format'

/** Money-at-stake items shown before "Show more" on a phone-sized list. */
const SHOWN = 3

/**
 * The items above the days, in queue order: anything on a clock, then money
 * at stake, then ideas on one line that never counts.
 */
export function ItemBand({ items }: { items: readonly ReviewItem[] }) {
  const actNow = items.filter((item) => item.tier === 'act_now')
  const atStake = items.filter((item) => item.tier === 'money_at_stake' || item.tier === 'tidy_up')
  const ideas = items.filter((item) => item.tier === 'ideas')
  const [showAll, setShowAll] = useState(false)
  const [showIdeas, setShowIdeas] = useState(false)
  const shown = showAll ? atStake : atStake.slice(0, SHOWN)
  const hidden = atStake.slice(shown.length)

  if (items.length === 0) return null

  return (
    <div className="review-band">
      {actNow.map((item) => (
        <UrgentItem key={item.key} item={item} />
      ))}

      {atStake.length > 0 ? (
        <section className="review-stake" aria-labelledby="review-stake-title">
          <header className="review-stake__head">
            <h3 id="review-stake-title" className="review-stake__title">
              Money at stake
            </h3>
            <span className="review-stake__count">{atStake.length}</span>
          </header>
          <ul className="review-stake__list">
            {shown.map((item) => (
              <StakeItem key={item.key} item={item} />
            ))}
          </ul>
          {hidden.length > 0 ? (
            <button type="button" className="review-stake__more" onClick={() => setShowAll(true)}>
              Show {hidden.length} more: {hidden.map((item) => item.title.toLowerCase()).join(', ')}
            </button>
          ) : null}
        </section>
      ) : null}

      {ideas.length > 0 ? (
        <section className="review-ideas">
          <button
            type="button"
            className="review-ideas__toggle"
            aria-expanded={showIdeas}
            onClick={() => setShowIdeas((open) => !open)}
          >
            {plural(ideas.length, 'idea')}, not counted
          </button>
          {showIdeas ? (
            <ul className="review-stake__list">
              {ideas.map((item) => (
                <StakeItem key={item.key} item={item} />
              ))}
            </ul>
          ) : null}
        </section>
      ) : null}
    </div>
  )
}

function dueWords(item: ReviewItem): string {
  if (!item.due_at) return item.detail
  const minutes = Math.round((new Date(item.due_at).getTime() - Date.now()) / 60_000)
  if (item.kind === 'sign_in_code') {
    return minutes > 0 ? `Expires in ${plural(minutes, 'min')}` : 'Expiring now'
  }
  return item.detail || relativeDay(item.due_at.slice(0, 10))
}

/** A sign-in code, a sign-in or a reconnect: the one tone that says "now". */
function UrgentItem({ item }: { item: ReviewItem }) {
  const navigate = useNavigate()
  const act = useReviewAct()
  const [code, setCode] = useState('')
  const answer = item.actions.find((one) => one.action === 'answer_code')

  return (
    <article className="review-urgent" aria-label={item.title}>
      <div className="review-urgent__text">
        <h3 className="review-urgent__title">{item.title}</h3>
        <p className="review-urgent__due">{dueWords(item)}</p>
      </div>
      {answer ? (
        <form
          className="review-urgent__code"
          onSubmit={(event) => {
            event.preventDefault()
            if (code.trim() === '') return
            act.mutate({ key: item.key, action: 'answer_code', text: code.trim() })
          }}
        >
          <Input
            aria-label="Code"
            inputMode="numeric"
            autoComplete="one-time-code"
            value={code}
            onChange={(event) => setCode(event.target.value)}
          />
          <Button type="submit" size="sm" disabled={act.isPending}>
            Send
          </Button>
        </form>
      ) : item.link ? (
        <Button size="sm" onClick={() => navigate(item.link)}>
          {item.kind === 'bank_reconnect' ? 'Reconnect' : 'Sign in'}
        </Button>
      ) : null}
    </article>
  )
}

function StakeItem({ item }: { item: ReviewItem }) {
  const navigate = useNavigate()
  const act = useReviewAct()
  const [asking, setAsking] = useState<ReviewActionOption | null>(null)

  const run = (option: ReviewActionOption) => {
    if (option.confirm) setAsking(option)
    else act.mutate({ key: item.key, action: option.action })
  }

  return (
    <li className="review-stake__item">
      <div className="review-stake__text">
        <span className="review-stake__name">{item.title}</span>
        <span className="review-stake__detail">{item.detail}</span>
      </div>
      {item.amount ? (
        <Money value={item.amount} className="review-stake__amount" tone="neutral" />
      ) : null}
      <div className="review-stake__actions">
        {item.actions.map((option) => (
          <Button
            key={option.action}
            size="sm"
            variant="secondary"
            disabled={act.isPending}
            onClick={() => run(option)}
          >
            {option.label}
          </Button>
        ))}
        {item.link ? (
          <Button size="sm" variant="ghost" onClick={() => navigate(item.link)}>
            Open
          </Button>
        ) : null}
      </div>
      <ConfirmDialog
        open={asking !== null}
        title={`${asking?.label ?? ''}?`}
        description={`${item.title}. This is hard to undo.`}
        confirmLabel={asking?.label ?? 'Confirm'}
        pending={act.isPending}
        onCancel={() => setAsking(null)}
        onConfirm={() => {
          if (!asking) return
          act.mutate({ key: item.key, action: asking.action }, { onSettled: () => setAsking(null) })
        }}
      />
    </li>
  )
}
