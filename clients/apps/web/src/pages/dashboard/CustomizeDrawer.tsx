/**
 * Customize Dashboard: an up/down pair and a switch per widget, plus *Reset to
 * defaults*. Reordering is the arrow buttons only, which the keyboard and a
 * screen reader can use.
 *
 * Edits apply to a draft and land on Done. The draft is seeded on each
 * opening, which keeps a cancelled reorder cancelled.
 */

import { ChevronDown, ChevronUp } from 'lucide-react'
import { Fragment, useMemo, useState } from 'react'

import { buildAccountTree, type AccountNode } from '@/components/shell/accountTree'
import {
  Button,
  Callout,
  Checklist,
  DialogActions,
  DialogContent,
  FormDialog,
  IconButton,
  List,
  ListRow,
  Switch,
} from '@/components/ui'
import type { AccountWithBalances, Uuid } from '@/lib/transactions/types'

import { everydayAccounts, recentAccountIds } from '@/lib/accountScope'
import {
  DEFAULT_LAYOUT,
  WIDGET_TITLES,
  moveWidget,
  setWidgetAccounts,
  toggleWidget,
  type WidgetLayout,
} from './layout'

export function CustomizeDrawer({
  open,
  onOpenChange,
  layout,
  accounts,
  onApply,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  layout: readonly WidgetLayout[]
  accounts: readonly AccountWithBalances[]
  onApply: (layout: WidgetLayout[]) => void
}) {
  return (
    <FormDialog open={open} onOpenChange={onOpenChange}>
      <CustomizeForm
        onOpenChange={onOpenChange}
        layout={layout}
        accounts={accounts}
        onApply={onApply}
      />
    </FormDialog>
  )
}

function CustomizeForm({
  onOpenChange,
  layout,
  accounts,
  onApply,
}: {
  onOpenChange: (open: boolean) => void
  layout: readonly WidgetLayout[]
  accounts: readonly AccountWithBalances[]
  onApply: (layout: WidgetLayout[]) => void
}) {
  const [draft, setDraft] = useState<WidgetLayout[]>([...layout])
  const tree = useMemo(() => buildAccountTree(accounts), [accounts])

  return (
    <DialogContent
      title="Customize dashboard"
      className="drawer-dialog"
      footer={
        <DialogActions
          onCancel={() => onOpenChange(false)}
          start={
            <Button variant="ghost" onClick={() => setDraft([...DEFAULT_LAYOUT])}>
              Reset to defaults
            </Button>
          }
        >
          <Button
            variant="primary"
            onClick={() => {
              onApply(draft)
              onOpenChange(false)
            }}
          >
            Save
          </Button>
        </DialogActions>
      }
    >
      <List>
        {draft.map((entry, index) => (
          <Fragment key={entry.id}>
            <ListRow
              title={
                <Switch
                  label={WIDGET_TITLES[entry.id]}
                  checked={entry.on}
                  onCheckedChange={() => setDraft((current) => toggleWidget(current, entry.id))}
                />
              }
              actions={
                <>
                  <IconButton
                    label={`Move ${WIDGET_TITLES[entry.id]} up`}
                    variant="ghost"
                    size="sm"
                    disabled={index === 0}
                    onClick={() => setDraft((current) => moveWidget(current, entry.id, -1))}
                  >
                    <ChevronUp size={13} />
                  </IconButton>
                  <IconButton
                    label={`Move ${WIDGET_TITLES[entry.id]} down`}
                    variant="ghost"
                    size="sm"
                    disabled={index === draft.length - 1}
                    onClick={() => setDraft((current) => moveWidget(current, entry.id, 1))}
                  >
                    <ChevronDown size={13} />
                  </IconButton>
                </>
              }
            />
            {/* Only for the widget that has one, and only while it is on. */}
            {entry.id === 'recent' && entry.on ? (
              <li className="panel-picker__entry">
                <RecentAccounts
                  tree={tree}
                  accounts={accounts}
                  chosen={entry.accounts}
                  onChange={(next) =>
                    setDraft((current) => setWidgetAccounts(current, 'recent', next))
                  }
                />
              </li>
            ) : null}
          </Fragment>
        ))}
      </List>
    </DialogContent>
  )
}

/**
 * Which accounts Recent Transactions reads, grouped as the accounts drawer
 * groups them (`buildAccountTree`), without closed accounts.
 *
 * The ticks show the resolved selection: with none stored, the default rule's
 * answer. The first change stores a selection, and *Use default* hands it back,
 * which is not the same as ticking every account.
 */
function RecentAccounts({
  tree,
  accounts,
  chosen,
  onChange,
}: {
  tree: readonly AccountNode[]
  accounts: readonly AccountWithBalances[]
  chosen: Uuid[] | undefined
  onChange: (accounts: readonly Uuid[] | null) => void
}) {
  const selected = recentAccountIds(chosen, accounts)
  const groups = tree.map((klass) => ({
    id: klass.id,
    label: klass.name,
    options: leavesOf(klass).map((account) => ({ id: account.id, label: account.name })),
  }))

  return (
    <div className="panel-picker__settings">
      <div className="panel-picker__settings-head">
        <p className="muted">Accounts shown</p>
        <Button
          variant="ghost"
          size="sm"
          disabled={chosen === undefined}
          onClick={() => onChange(null)}
        >
          Use default (everyday accounts)
        </Button>
      </div>
      <Checklist groups={groups} chosen={selected} onChange={onChange} empty="No open accounts." />
      {selected.length === 0 ? (
        <Callout tone="warning">
          None ticked: the widget stays empty. Default: {everydayAccounts(accounts).length}{' '}
          accounts.
        </Callout>
      ) : null}
    </div>
  )
}

/** The accounts under one class, flattened past the group rows: a third level of headings costs more indent than it buys here. */
function leavesOf(node: AccountNode): AccountNode[] {
  if (node.kind === 'account') return [node]
  return (node.children ?? []).flatMap(leavesOf)
}
