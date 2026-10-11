import { Sparkles } from 'lucide-react'

import { useMoneyText } from '@/components/moneyText'
import { Meter } from '@/components/ui'
import { useMerchantMatch } from '@/lib/clients/merchant'
import { formatPercent, timeAgo } from '@/lib/format'
import { merchantsFor } from '@/lib/merchants'
import type { Money } from '@/lib/money'
import {
  basisLabel,
  confidenceBand,
  suggestedCategoryId,
  suggestionReason,
  suggestionSource,
} from '@/lib/transactions/suggestions'
import type { Suggestion, Transaction, Uuid } from '@/lib/transactions/types'

/** The confidence as a word, a figure and a bar; a note when none was recorded. */
export function SuggestionConfidence({ confidence }: { confidence: number | null }) {
  if (confidence === null) {
    return <p className="suggestion-confidence suggestion-confidence--none">No confidence recorded</p>
  }
  const band = confidenceBand(confidence)
  const percent = formatPercent(confidence, { digits: 0 })
  return (
    <div className="suggestion-confidence">
      <div className="suggestion-confidence__line">
        <span className="suggestion-confidence__label">Confidence</span>
        <span className={`suggestion-confidence__value suggestion-confidence__value--${band.tone}`}>
          {band.label} · {percent}
        </span>
      </div>
      <Meter
        size="sm"
        segments={[{ value: confidence * 100, tone: band.tone }]}
        label="Confidence"
        valueText={`${band.label}, ${percent}`}
      />
    </div>
  )
}

/** What decided the category, as a tag, and the proposal's own sentence. */
export function SuggestionBasisLine({ label, sentence }: { label: string; sentence: string }) {
  return (
    <p className="suggestion-basis">
      <span className="suggestion-basis__tag">{label}</span>
      {sentence ? <span className="suggestion-basis__text">{sentence}</span> : null}
    </p>
  )
}

export interface SuggestionLine {
  categoryId: Uuid | null
  confidence: number | null
  items: string[]
  amount: Money | null
}

/**
 * What a proposal would file, line by line: each category with how sure the
 * proposal is of it, the items it covers and its amount; then what decided it.
 * The hover card and the run dialog both draw it.
 */
export function SuggestionSummary({
  split,
  lines,
  confidence,
  categoryName,
  basis,
  sentence,
}: {
  split: boolean
  lines: SuggestionLine[]
  confidence: number | null
  categoryName: (id: Uuid | null) => string
  basis: string
  sentence: string
}) {
  const moneyText = useMoneyText()
  return (
    <>
      <header className="suggestion-card__head">
        <span className="suggestion-card__eyebrow">
          <Sparkles size={12} aria-hidden="true" />
          {split ? 'Suggested split' : 'Suggested category'}
        </span>
        <strong className="suggestion-card__title">
          {split
            ? `${lines.length || 'Several'} categories`
            : categoryName(lines[0]?.categoryId ?? null)}
        </strong>
      </header>

      <SuggestionConfidence confidence={confidence} />

      {lines.length > 0 ? (
        <ul className="suggestion-card__lines">
          {lines.map((line, index) => (
            <li key={index}>
              <span className="suggestion-card__line-name">
                <span>
                  {categoryName(line.categoryId)}
                  {line.confidence === null ? null : (
                    <span className="suggestion-card__line-confidence">
                      {' '}
                      ({formatPercent(line.confidence, { digits: 0 })})
                    </span>
                  )}
                </span>
                {line.items.map((item, i) => (
                  <span key={i} className="suggestion-card__item">
                    {item}
                  </span>
                ))}
              </span>
              {line.amount === null ? null : (
                <span className="suggestion-card__line-amount">{moneyText(line.amount)}</span>
              )}
            </li>
          ))}
        </ul>
      ) : null}

      <SuggestionBasisLine label={basis} sentence={sentence} />
    </>
  )
}

/** The hover card over a waiting suggestion in the register. */
export function SuggestionCard({
  txn,
  suggestion,
  categoryName,
  onShowRun,
}: {
  txn: Transaction
  suggestion: Suggestion
  categoryName: (id: Uuid | null) => string
  onShowRun?: (runId: Uuid) => void
}) {
  const proposed = suggestedCategoryId(suggestion)
  // A whole-row proposal carries no items; the order behind the row has them.
  const order = useMerchantMatch(
    proposed !== undefined && merchantsFor(txn).length > 0 ? txn.id : null,
  )
  const runId = suggestion.run_id

  const lines: SuggestionLine[] =
    proposed === undefined
      ? suggestion.splits.map((split) => ({
          categoryId: split.category_id,
          confidence: split.confidence,
          items: split.memo ? split.memo.split(', ') : [],
          amount: split.amount,
        }))
      : [
          {
            categoryId: proposed,
            confidence: suggestion.confidence,
            items: (order.data?.orders ?? []).flatMap(({ order: one }) =>
              one.items.map((item) => item.catalog?.title || item.title),
            ),
            amount: txn.amount,
          },
        ]

  return (
    <div className="suggestion-card">
      <SuggestionSummary
        split={proposed === undefined}
        lines={lines}
        confidence={suggestion.confidence}
        categoryName={categoryName}
        basis={basisLabel(suggestion)}
        sentence={suggestionReason(suggestion.summary)}
      />
      <footer className="suggestion-card__foot">
        <span>
          {suggestionSource(suggestion)} · {timeAgo(suggestion.created_at, 'long')}
        </span>
        {runId !== null && onShowRun ? (
          <button type="button" className="suggestion-card__run" onClick={() => onShowRun(runId)}>
            See the run
          </button>
        ) : null}
      </footer>
    </div>
  )
}
