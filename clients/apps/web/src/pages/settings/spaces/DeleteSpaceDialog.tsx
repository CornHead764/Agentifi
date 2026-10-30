import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'

import {
  Button,
  Callout,
  Dialog,
  DialogActions,
  DialogContent,
  Field,
  Input,
  useToast,
} from '@/components/ui'
import { useAuth } from '@/contexts/auth'
import { ApiError } from '@/lib/api'
import {
  canDeleteSpace,
  confirmsSpaceName,
  useDeleteSpace,
  type Space,
} from '@/lib/clients/spaces'

/**
 * Deleting a space, which no undo reaches: the name typed out, what goes with
 * it said plainly, and the backup the server takes first named. `onDeleted`
 * gets the space to open next when the one deleted was being read.
 */
export function DeleteSpaceDialog({
  space,
  spaces,
  onOpenChange,
  onDeleted,
}: {
  space: Space | null
  spaces: readonly Space[]
  onOpenChange: (open: boolean) => void
  onDeleted: (deleted: Space, next: Space) => void
}) {
  return (
    <Dialog open={space !== null} onOpenChange={onOpenChange}>
      {space ? (
        <DeleteSpaceBody
          key={space.id}
          space={space}
          spaces={spaces}
          onCancel={() => onOpenChange(false)}
          onDeleted={onDeleted}
        />
      ) : null}
    </Dialog>
  )
}

function DeleteSpaceBody({
  space,
  spaces,
  onCancel,
  onDeleted,
}: {
  space: Space
  spaces: readonly Space[]
  onCancel: () => void
  onDeleted: (deleted: Space, next: Space) => void
}) {
  const { user } = useAuth()
  const { show } = useToast()
  const remove = useDeleteSpace()
  const [typed, setTyped] = useState('')
  const [error, setError] = useState<string | null>(null)

  const allowed = canDeleteSpace(space, spaces.length)
  const confirmed = confirmsSpaceName(typed, space.name)
  const next = spaces.find((one) => one.id !== space.id) ?? null

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!allowed || !confirmed || next === null) return
    setError(null)
    remove.mutate(
      { id: space.id, confirmName: typed },
      {
        onSuccess: (deleted) => {
          show({
            title: `${space.name} deleted`,
            description: deleted.backup_set
              ? `Backup set ${deleted.backup_set} was taken first.`
              : undefined,
            tone: 'success',
          })
          onDeleted(space, next)
        },
        onError: (failure) =>
          setError(
            failure instanceof ApiError
              ? (failure.detail ?? 'The space was not deleted.')
              : 'Could not reach the server.',
          ),
      },
    )
  }

  return (
    <DialogContent
      title={`Delete ${space.name}?`}
      description="This cannot be undone from the app."
      onSubmit={submit}
      footer={
        <DialogActions onCancel={onCancel} cancelDisabled={remove.isPending}>
          <Button
            type="submit"
            variant="danger"
            disabled={!allowed || !confirmed || remove.isPending}
          >
            {remove.isPending ? 'Backing up and deleting…' : 'Delete space'}
          </Button>
        </DialogActions>
      }
    >
      <div className="stack">
        <p>
          Every account, transaction, budget, rule, goal, bill, attachment and connection in this
          space is deleted for good, for everyone who shares it. Bank and bill provider sign-ins
          stored here are deleted with it.
        </p>
        <Callout tone="warning">
          <p>
            Where this server writes backups, it takes a backup set before deleting, and a backup
            that fails deletes nothing. Getting the space back means restoring the whole server from
            that set, which also undoes every change made in any space since.
            {user?.is_superuser ? (
              <>
                {' '}
                Check the backups are on and encrypted under{' '}
                <Link to="/settings/admin">Server admin</Link> first.
              </>
            ) : (
              ' Ask whoever runs the server whether backups are on before going ahead.'
            )}
          </p>
        </Callout>
        {allowed ? (
          <Field label={`Type ${space.name} to confirm`}>
            <Input
              value={typed}
              autoComplete="off"
              spellCheck={false}
              autoFocus
              onChange={(event) => setTyped(event.target.value)}
            />
          </Field>
        ) : (
          <Callout tone="warning">
            This is your only space. Create another before deleting this one.
          </Callout>
        )}
        {error ? (
          <Callout tone="expense" role="alert">
            {error}
          </Callout>
        ) : null}
      </div>
    </DialogContent>
  )
}
