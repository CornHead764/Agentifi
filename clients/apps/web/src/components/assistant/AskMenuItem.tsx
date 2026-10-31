/**
 * "Ask…", wherever a menu is about a row, in three shapes: a `DropdownMenu`
 * item, a `ContextMenu` item, and a plain button.
 *
 * Each renders nothing when the assistant is dormant. Not the sparkle icon,
 * which marks what the assistant proposes on its own. The subject is often a
 * thunk, so nothing is looked up for a menu nobody opened.
 */

import { MessageCircleQuestion } from 'lucide-react'

import {
  Button,
  ContextMenuItem,
  DropdownMenuItem,
  type ButtonSize,
  type ButtonVariant,
} from '@/components/ui'
import { useAskAssistant } from '@/contexts/askAssistant'
import type { AskSubject } from '@/lib/assistant/rowQuestion'

type Subject = AskSubject | (() => AskSubject)

function resolve(subject: Subject): AskSubject {
  return typeof subject === 'function' ? subject() : subject
}

/** The kebab-menu form, for every row that has one. */
export function AskMenuItem({ subject }: { subject: Subject }) {
  const { ready, ask } = useAskAssistant()
  if (!ready) return null
  return (
    <DropdownMenuItem
      icon={<MessageCircleQuestion size={14} />}
      onSelect={() => ask(resolve(subject))}
    >
      Ask…
    </DropdownMenuItem>
  )
}

/** The right-click form, for the register's account header. */
export function AskContextMenuItem({ subject }: { subject: Subject }) {
  const { ready, ask } = useAskAssistant()
  if (!ready) return null
  return (
    <ContextMenuItem
      icon={<MessageCircleQuestion size={14} />}
      onSelect={() => ask(resolve(subject))}
    >
      Ask…
    </ContextMenuItem>
  )
}

/** The button form, for a dialog or a header that has no menu to hang it on. */
export function AskRowButton({
  subject,
  size = 'sm',
  variant = 'ghost',
}: {
  subject: Subject
  size?: ButtonSize
  variant?: ButtonVariant
}) {
  const { ready, ask } = useAskAssistant()
  if (!ready) return null
  return (
    <Button size={size} variant={variant} type="button" onClick={() => ask(resolve(subject))}>
      <MessageCircleQuestion size={13} aria-hidden="true" /> Ask
    </Button>
  )
}
