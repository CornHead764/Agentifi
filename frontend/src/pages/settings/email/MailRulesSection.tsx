import { Pencil, Plus, Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'

import { QueryBoundary } from '@/components/QueryBoundary'
import { Button, Card, ConfirmDialog, EmptyState, Switch, useConfirm } from '@/components/ui'
import { connectionTitle } from '@/lib/billers'
import { useAllBillSubaccounts, useBillConnections } from '@/lib/clients/bills'
import {
  useDeleteMailRule,
  useMailRules,
  useUpdateMailRule,
  type MailRule,
} from '@/lib/clients/email'
import { useAccounts, useCategories } from '@/lib/transactions/queries'

import { ConnectorList, ConnectorRow } from '../connector/ConnectorRow'
import { describeMailRuleAction, describeMailRuleMatch, type MailRuleNames } from './mailRule'
import { MailRuleDialog } from './MailRuleDialog'

/**
 * The household's own mail parsers. They sit on the mailbox card rather than
 * beside the transaction rules because they match a mail, not a register row.
 */
export function MailRulesSection() {
  const rules = useMailRules()
  const accounts = useAccounts()
  const categories = useCategories()
  const providers = useBillConnections()
  const billed = useAllBillSubaccounts()
  const update = useUpdateMailRule()
  const remove = useConfirm(useDeleteMailRule(), {
    variables: (rule: MailRule) => rule.id,
  })

  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<MailRule | null>(null)

  const names = useMemo<MailRuleNames>(() => {
    const byAccount = new Map((accounts.data ?? []).map((one) => [one.id, one.name]))
    const byCategory = new Map((categories.data ?? []).map((one) => [one.id, one.name]))
    const byProvider = new Map((providers.data ?? []).map((one) => [one.id, connectionTitle(one)]))
    const byBilled = new Map((billed.data ?? []).map((one) => [one.id, one.label]))
    return {
      account: (id) => (id === null ? undefined : byAccount.get(id)),
      category: (id) => (id === null ? undefined : byCategory.get(id)),
      provider: (id) => (id === null ? undefined : byProvider.get(id)),
      billedAccount: (id) => (id === null ? undefined : byBilled.get(id)),
    }
  }, [accounts.data, categories.data, providers.data, billed.data])

  return (
    <Card
      id="mail-rules"
      title="Mail rules"
      subtitle="For mail no built-in parser reads. The first rule that matches wins."
      actions={
        <Button size="sm" variant="secondary" onClick={() => setAdding(true)}>
          <Plus size={13} aria-hidden="true" /> Add rule
        </Button>
      }
    >

      <QueryBoundary
        query={rules}
        rows={2}
        empty={(rows) =>
          rows.length === 0 ? (
            <EmptyState
              compact
              title="No rules yet. A rule turns a receipt into a transaction, or files a mailed bill on a bill provider."
            />
          ) : undefined
        }
      >
        {(rows) => (
          <ConnectorList>
            {rows.map((rule) => (
              <ConnectorRow
                key={rule.id}
                title={rule.name}
                meta={[describeMailRuleMatch(rule)]}
                note={<p className="connector__line">{describeMailRuleAction(rule, names)}</p>}
                aside={
                  <Switch
                    checked={rule.enabled}
                    aria-label={`Run ${rule.name}`}
                    onCheckedChange={(enabled) => update.mutate({ id: rule.id, patch: { enabled } })}
                  />
                }
                actions={[
                  { label: 'Edit', icon: <Pencil size={14} />, onSelect: () => setEditing(rule) },
                  {
                    label: 'Remove',
                    icon: <Trash2 size={14} />,
                    danger: true,
                    onSelect: () => remove.ask(rule),
                  },
                ]}
                actionsLabel={`Actions for ${rule.name}`}
                dimmed={!rule.enabled}
              />
            ))}
          </ConnectorList>
        )}
      </QueryBoundary>

      {adding ? (
        <MailRuleDialog rule={null} onClose={() => setAdding(false)} />
      ) : null}
      {editing ? (
        <MailRuleDialog rule={editing} onClose={() => setEditing(null)} />
      ) : null}

      <ConfirmDialog
        {...remove.dialog}
        title={remove.target ? `Remove ${remove.target.name}?` : 'Remove this mail rule?'}
        description="Transactions and bills it already wrote stay. New mail it would have claimed is left in the log."
        confirmLabel="Remove rule"
      />
    </Card>
  )
}
