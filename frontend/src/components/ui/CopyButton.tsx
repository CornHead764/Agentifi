import { Copy } from 'lucide-react'

import { Button, type ButtonProps } from './Button'
import { useCopy } from './copy'

export interface CopyButtonProps extends Omit<ButtonProps, 'onClick' | 'children'> {
  text: string
  label?: string
  /** The toast's title once it is on the clipboard. */
  copied?: string
}

export function CopyButton({ text, label = 'Copy', copied, ...props }: CopyButtonProps) {
  const copy = useCopy()
  return (
    <Button {...props} onClick={() => copy(text, copied)}>
      <Copy size={13} aria-hidden="true" /> {label}
    </Button>
  )
}

/** A value to be copied exactly: a password shown once, a callback URL. */
export function CopyableSecret({ value }: { value: string }) {
  return (
    <div className="admin-secret">
      <code className="admin-secret__value">{value}</code>
      <CopyButton text={value} />
    </div>
  )
}
