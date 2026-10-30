import { ListChecks } from 'lucide-react'
import { useMemo, useState } from 'react'

import { RuleCreateFlow } from '@/components/transactions/CreateFromTransaction'
import { useToast } from '@/components/ui'
import {
  useDeleteRule,
  useReorderRules,
  useRules,
  useSetRuleActive,
  useUpdateRule,
  type Rule,
} from '@/lib/clients/rules'
import { plural } from '@/lib/format'
import { useLinkedItem, useScrollToLinked } from '@/lib/linkedItem'
import { useAccounts, useCategories, useTags } from '@/lib/transactions/queries'
import type { Uuid } from '@/lib/transactions/types'

import { ListPanel } from './ListPanel'
import { RuleEditor } from './RuleEditor'
import { ChipLine } from './RuleList'
import { RuleReviewDialog } from './RuleReviewDialog'
import { describeActions } from './conditions'

/**
 * The rules tab.
 *
 * **The order decides which rule wins.** Rules run top to bottom and the first
 * to set a field keeps it, so rows carry move controls rather than sorting by
 * name.
 *
 * **Saving never rewrites history**; *Review existing transactions* is the
 * separate step, with a per-row diff, and a new rule goes straight on to it.
 * **Switching a rule off is not an undo**: what it already did stays.
 */
export function RulesPanel({
  creating,
  onCreatingChange,
}: {
  /** The new-rule flow, opened from the page header. */
  creating: boolean
  onCreatingChange: (open: boolean) => void
}) {
  const { show } = useToast()

  const rules = useRules()
  const accounts = useAccounts()
  const categories = useCategories()
  const tags = useTags()

  const update = useUpdateRule()

  const [editing, setEditing] = useState<{ rule: Rule | null } | null>(null)
  const [reviewing, setReviewing] = useState<Uuid | null>(null)

  const rows = rules.data ?? []
  const names = useMemo(
    () =>
      new Map<string, string>([
        ...(categories.data ?? []).map((category) => [category.id, category.name] as const),
        ...(tags.data ?? []).map((tag) => [tag.id, tag.name] as const),
      ]),
    [categories.data, tags.data],
  )
  const underReview = rows.find((rule) => rule.id === reviewing) ?? null

  // `/rules?rule=<id>`, where an accepted assistant card leads: the row is
  // marked and scrolled to, and its editor opens once.
  const link = useLinkedItem('rule')
  useScrollToLinked(link.linked, rules.isSuccess)
  const linkedRule =
    link.opening === null ? null : (rows.find((rule) => rule.id === link.opening) ?? null)
  const shown =
    editing ??
    (creating ? { rule: null } : linkedRule === null ? null : { rule: linkedRule })
  const opened = shown?.rule ?? null

  return (
    <ListPanel
      kind="rule"
      query={rules}
      rows={rows}
      then={(rule) => <ChipLine texts={describeActions(rule.actions, names)} />}
      menu={(rule) => [
        {
          label: 'Review existing transactions',
          icon: <ListChecks size={14} />,
          onSelect: () => setReviewing(rule.id),
        },
      ]}
      linkedId={link.linked}
      reorder={useReorderRules()}
      setActive={useSetRuleActive()}
      remove={useDeleteRule()}
      onEdit={(rule) => setEditing({ rule })}
    >
      {shown === null ? null : opened === null ? (
        <RuleCreateFlow
          accounts={accounts.data ?? []}
          categories={categories.data ?? []}
          tags={tags.data ?? []}
          onClose={() => {
            setEditing(null)
            onCreatingChange(false)
          }}
        />
      ) : (
        <RuleEditor
          rule={opened}
          accounts={accounts.data ?? []}
          categories={categories.data ?? []}
          tags={tags.data ?? []}
          pending={update.isPending}
          onSubmit={(body) => update.mutateAsync({ id: opened.id, patch: body })}
          onClose={() => {
            setEditing(null)
            link.settle()
          }}
        />
      )}

      {underReview ? (
        <RuleReviewDialog
          rule={underReview}
          categories={categories.data ?? []}
          tags={tags.data ?? []}
          onApplied={(count) =>
            show({
              title: `${plural(count, 'transaction')} updated`,
              tone: 'success',
            })
          }
          onClose={() => setReviewing(null)}
        />
      ) : null}
    </ListPanel>
  )
}
