import { Button, Dialog, DialogActions, DialogContent } from '@/components/ui'
import type { CategoryNode } from '@/lib/categoryTree'
import { useCategoryUsage } from '@/lib/clients/categories'
import type { Category } from '@/lib/transactions/types'

import { categoryDeleteWarning, strandedSubcategoryWarning } from './warnings'

/**
 * What deleting a category does, said before it happens: its transactions
 * keep it, and its subcategories reappear at the top level. A failed count
 * says the count is unknown, since silence would read as "nothing".
 */
export function CategoryDeleteDialog({
  node,
  pending,
  onConfirm,
  onClose,
}: {
  node: CategoryNode<Category>
  pending: boolean
  onConfirm: () => void
  onClose: () => void
}) {
  const usage = useCategoryUsage(node.category.id)
  const stranded = strandedSubcategoryWarning(node.children.map((child) => child.category.name))

  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent
        title={`Delete ${node.category.name}?`}
        description="No longer offered for new rows. Rows already filed under it are not re-filed."
        footer={
          <DialogActions>
            <Button variant="danger" onClick={onConfirm} disabled={pending}>
              Delete category
            </Button>
          </DialogActions>
        }
      >
        <p className="hint">
          {categoryDeleteWarning({ isError: usage.isError, count: usage.data })}
        </p>
        {stranded === null ? null : <p className="hint">{stranded}</p>}
      </DialogContent>
    </Dialog>
  )
}
