import { X } from 'lucide-react'
import { useState, type KeyboardEvent } from 'react'

import { FilterFacets } from '@/components/transactions/FilterFacets'
import { BUILDER_FACETS } from '@/components/transactions/facets'
import { Button, Field, IconButton, Input, OptionSelect } from '@/components/ui'
import type { Account, Category, Tag } from '@/lib/transactions/types'

import { distinctKeywords, toNameOperator, type ConditionDraft } from './conditions'

/**
 * *If a transaction matches this*: the column shared by the rule and guidance
 * editors, so both ask "which transactions?" in the same words (ground rule 3).
 *
 * The name half (match on, a comparison, keywords) is this column's own,
 * because keyword sets that are alternatives exist nowhere else. Everything
 * else is `FilterFacets`, the register's Filter popover.
 *
 * **Original statement name is the recommended field.** The bank's wording
 * never changes, so conditions on it keep matching after a rule's own rename
 * lands.
 */
export function ConditionColumn({
  draft,
  accounts,
  categories,
  tags,
  onChange,
}: {
  draft: ConditionDraft
  accounts: readonly Account[]
  categories: readonly Category[]
  tags: readonly Tag[]
  onChange: (draft: ConditionDraft) => void
}) {
  const setGroup = (index: number, keywords: string[]) =>
    onChange({
      ...draft,
      keywordGroups: draft.keywordGroups.map((group, i) => (i === index ? keywords : group)),
    })

  return (
    <>
      <Field
        label="Match on"
        hint={
          draft.nameField === 'statement_name'
            ? 'Recommended: the bank’s name, which never changes.'
            : 'The cleaned name. If this rule renames it, it can stop matching.'
        }
      >
        <OptionSelect
          value={draft.nameField}
          onValueChange={(value) =>
            onChange({ ...draft, nameField: value === 'payee' ? 'payee' : 'statement_name' })
          }
          options={[
            { value: 'statement_name', label: 'Original statement name' },
            { value: 'payee', label: 'Payee' },
          ]}
        />
      </Field>

      <Field
        label="Comparison"
        hint={
          draft.nameOperator === 'matches'
            ? 'Each entry is a regular expression; any one matching is enough.'
            : undefined
        }
      >
        <OptionSelect
          value={draft.nameOperator}
          onValueChange={(value) => onChange({ ...draft, nameOperator: toNameOperator(value) })}
          options={[
            { value: 'contains', label: 'Contains' },
            { value: 'is_exactly', label: 'Is exactly' },
            { value: 'matches', label: 'Matches a pattern' },
          ]}
        />
      </Field>

      {draft.keywordGroups.map((keywords, index) => (
        <Field
          // The groups have no identity of their own; the index is what the
          // builder's + and × move.
          key={index}
          as="group"
          label={index === 0 ? 'Keywords' : 'Or these keywords'}
          hint={index === 0 ? 'Every keyword in a set has to be present.' : undefined}
        >
          <KeywordChips
            keywords={keywords}
            onChange={(next) => setGroup(index, next)}
            onRemoveGroup={
              draft.keywordGroups.length === 1
                ? undefined
                : () =>
                    onChange({
                      ...draft,
                      keywordGroups: draft.keywordGroups.filter((_, i) => i !== index),
                    })
            }
          />
        </Field>
      ))}
      <Button onClick={() => onChange({ ...draft, keywordGroups: [...draft.keywordGroups, []] })}>
        Add an alternative
      </Button>

        {/* Whatever is chosen here narrows every keyword alternative above
            (see `buildConditions`); a facet on its own would be an alternative
            rather than a condition. */}
      <Field label="And also" as="group" hint="Leave a facet untouched to ignore it.">
        <div className="rule-builder__facets">
          <FilterFacets
            draft={draft.facets}
            categories={categories}
            tags={tags}
            accounts={accounts}
            facets={BUILDER_FACETS}
            offerVerdictFacets={false}
            compact
            onChange={(facets) => onChange({ ...draft, facets })}
          />
        </div>
      </Field>
    </>
  )
}

function KeywordChips({
  keywords,
  onChange,
  onRemoveGroup,
}: {
  keywords: string[]
  onChange: (keywords: string[]) => void
  onRemoveGroup?: () => void
}) {
  const [typed, setTyped] = useState('')

  const commit = () => {
    const next = distinctKeywords([...keywords, typed])
    setTyped('')
    if (next.length > keywords.length) onChange(next)
  }

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      event.preventDefault()
      commit()
    }
    if (event.key === 'Backspace' && typed === '' && keywords.length > 0) {
      onChange(keywords.slice(0, -1))
    }
  }

  return (
    <div className="stack stack--2 rule-keywords">
      <div className="chips chips--wrap">
        {/* Keyed by the word, which `distinctKeywords` makes safe: a repeated
            key stops the list reconciling. */}
        {keywords.map((keyword) => (
          <span key={keyword} className="chip chip--on chip--removable">
            {keyword}
            <IconButton
              size="sm"
              variant="ghost"
              label={`Remove ${keyword}`}
              onClick={() => onChange(keywords.filter((word) => word !== keyword))}
            >
              <X size={12} />
            </IconButton>
          </span>
        ))}
      </div>
      <div className="row rule-keywords__entry">
        <Input
          value={typed}
          onChange={(event) => setTyped(event.target.value)}
          onKeyDown={onKeyDown}
          onBlur={commit}
          placeholder="STREAMING"
          autoComplete="off"
        />
        {onRemoveGroup ? (
          <IconButton variant="ghost" label="Remove this alternative" onClick={onRemoveGroup}>
            <X size={14} />
          </IconButton>
        ) : null}
      </div>
    </div>
  )
}
