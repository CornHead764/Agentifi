import { useState, type ReactNode } from 'react'

import { Button } from './Button'
import { DialogClose, DialogContent } from './Dialog'
import { Field } from './Field'
import { FormDialog } from './FormDialog'
import { Input } from './Input'

export interface NameDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: string
  /** The field's visible label. */
  label?: string
  hint?: string
  placeholder?: string
  initial?: string
  /** An optional name may be saved blank, which the server reads as "use the default". */
  optional?: boolean
  maxLength?: number
  submitLabel?: string
  /**
   * Saves the trimmed name. The dialog closes when it resolves and stays open
   * when it rejects; the mutation behind it shows the failure.
   */
  onSubmit: (name: string) => Promise<unknown>
  /** More fields, below the name. */
  children?: ReactNode
}

/** Naming or renaming one thing. */
export function NameDialog({ open, onOpenChange, ...form }: NameDialogProps) {
  return (
    <FormDialog open={open} onOpenChange={onOpenChange}>
      <NameForm {...form} onDone={() => onOpenChange(false)} />
    </FormDialog>
  )
}

function NameForm({
  title,
  description,
  label = 'Name',
  hint,
  placeholder,
  initial = '',
  optional = false,
  maxLength,
  submitLabel = 'Save',
  onSubmit,
  children,
  onDone,
}: Omit<NameDialogProps, 'open' | 'onOpenChange'> & { onDone: () => void }) {
  const [name, setName] = useState(initial)
  const [pending, setPending] = useState(false)
  const trimmed = name.trim()

  const submit = () => {
    if (pending || (!optional && trimmed === '')) return
    setPending(true)
    onSubmit(trimmed).then(onDone, () => setPending(false))
  }

  return (
    <DialogContent
      title={title}
      description={description}
      onSubmit={submit}
      footer={
        <>
          <DialogClose asChild>
            <Button type="button">Cancel</Button>
          </DialogClose>
          <Button
            type="submit"
            variant="primary"
            disabled={pending || (!optional && trimmed === '')}
          >
            {pending ? 'Saving…' : submitLabel}
          </Button>
        </>
      }
    >
      <Field label={label} hint={hint}>
        <Input
          value={name}
          placeholder={placeholder}
          maxLength={maxLength}
          autoComplete="off"
          autoFocus
          onChange={(event) => setName(event.target.value)}
        />
      </Field>
      {children}
    </DialogContent>
  )
}
