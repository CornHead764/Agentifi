import { Facts } from '@/components/Facts'
import { Money } from '@/components/Money'
import { QueryBoundary } from '@/components/QueryBoundary'
import {
  Badge,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  EmptyState,
  Table,
  Td,
  Th,
} from '@/components/ui'
import { useApplyRule, useRulePreview, type Rule } from '@/lib/clients/rules'
import { plural } from '@/lib/format'
import type { Category, Tag } from '@/lib/transactions/types'

import { describeActions } from './conditions'

/**
 * *Continue to review*: the only way a saved rule reaches rows that already
 * exist, so reaching them is a decision rather than a side effect of saving.
 *
 * **Matched** is every row the conditions select; **will change** is the
 * subset the actions move. Showing only the first would read as every matched
 * row about to be rewritten.
 *
 * The apply sends the ids this list showed, so a row that synced while the
 * dialog was open is not swept in.
 */
export interface RuleReviewDialogProps {
  rule: Rule
  categories: readonly Category[]
  tags: readonly Tag[]
  onApplied: (count: number) => void
  onClose: () => void
}

export function RuleReviewDialog({
  rule,
  categories,
  tags,
  onApplied,
  onClose,
}: RuleReviewDialogProps) {
  const preview = useRulePreview(rule.id)
  const apply = useApplyRule()

  const changes = preview.data?.changes ?? []
  const names = new Map<string, string>([
    ...categories.map((category) => [category.id, category.name] as const),
    ...tags.map((tag) => [tag.id, tag.name] as const),
  ])

  const commit = () => {
    void apply
      .mutateAsync({
        id: rule.id,
        transactionIds: changes.map((change) => change.transaction_id),
      })
      .then((result) => {
        onApplied(result.applied)
        onClose()
      }, () => undefined)
  }

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        wide
        title={`Review existing transactions for ${rule.name}`}
        description="A preview. Nothing is changed until you apply."
        footer={
          <DialogActions>
            <Button
              variant="primary"
              onClick={commit}
              disabled={apply.isPending || changes.length === 0}
            >
              {changes.length === 0
                ? 'Nothing to apply'
                : `Apply to ${plural(changes.length, 'transaction')}`}
            </Button>
          </DialogActions>
        }
      >
        <QueryBoundary query={preview} rows={4}>
          {(data) => (
            <>
              <Facts
                facts={[
                  { label: 'Matched', value: data.matched },
                  {
                    label: 'Will change',
                    value: data.truncated ? `${data.changed} (first 500 shown)` : data.changed,
                  },
                  { label: 'Already match', value: data.unchanged },
                ]}
              />

              {changes.length === 0 ? (
                <EmptyState
                  title="Nothing to change"
                  body={
                    data.matched === 0
                      ? 'No existing transaction matches. The rule still runs on new ones.'
                      : 'Every matching transaction already looks like this.'
                  }
                />
              ) : null}

              {changes.length > 0 ? (
                <Table density="sm">
                  <thead>
                    <tr>
                      <Th>Date</Th>
                      <Th>Statement name</Th>
                      <Th>Account</Th>
                      <Th numeric>Amount</Th>
                      <Th>Would change</Th>
                    </tr>
                  </thead>
                  <tbody>
                    {changes.map((change) => (
                      <tr key={change.transaction_id}>
                        <Td>{change.date}</Td>
                        <Td>
                          <span className="rule-review__names">
                            <span className="rule-review__statement">{change.statement_name}</span>
                            <span className="muted">{change.payee}</span>
                          </span>
                        </Td>
                        <Td>{change.account_name}</Td>
                        <Td numeric>
                          <Money value={change.amount} />
                        </Td>
                        <Td>
                          <span className="chips chips--wrap">
                            {describeActions(change.actions, names).map((text) => (
                              <Badge key={text}>{text}</Badge>
                            ))}
                          </span>
                        </Td>
                      </tr>
                    ))}
                  </tbody>
                </Table>
              ) : null}
            </>
          )}
        </QueryBoundary>
      </DialogContent>
    </Dialog>
  )
}
