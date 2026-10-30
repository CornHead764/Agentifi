import { MoreVertical } from 'lucide-react'
import { Fragment, isValidElement, type ComponentProps, type ReactElement, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { IconButton } from './Button'
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from './DropdownMenu'
import { Spinner } from './Spinner'

/** Something the menu does. */
export interface OverflowAction {
  label: ReactNode
  icon?: ReactNode
  onSelect?: () => void
  /** A link out, opened in a new tab, rather than something done here. */
  href?: string
  /** A route in this app. */
  to?: string
  /** A submenu of further actions, for a choice that needs a second answer. */
  items?: readonly OverflowAction[]
  disabled?: boolean
  /** Why it is disabled, read on hover. */
  title?: string
  /** Destructive: drawn last, under a rule, in the danger colour. */
  danger?: boolean
}

/** A standing setting the menu toggles. */
export interface OverflowCheck {
  label: ReactNode
  icon?: ReactNode
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}

/**
 * One entry. An element is a ready-made item (`AskMenuItem`); `false`, `null`
 * and `undefined` are dropped, so an entry can be conditional in place.
 */
export type OverflowEntry = OverflowAction | OverflowCheck | ReactElement | false | null | undefined

/** A block under its own rule, optionally named ("Exclude from"). */
export interface OverflowSection {
  label?: string
  entries: readonly OverflowEntry[]
}

export interface OverflowMenuProps {
  /** Names the thing the menu acts on: "Actions for Groceries". */
  label: string
  actions: readonly OverflowEntry[]
  sections?: readonly OverflowSection[]
  /** Swaps the icon for a spinner while something the menu started runs. */
  busy?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
  /**
   * No button: the menu opens against an invisible anchor, from a gesture
   * elsewhere. The anchor stays in the DOM, because Radix positions against
   * its box and a `display: none` trigger has none.
   */
  anchorOnly?: boolean
  /**
   * With `anchorOnly`: open at this viewport point (a right-click) rather
   * than at the anchor's usual place.
   */
  anchorPoint?: { x: number; y: number }
}

/** The icon button every overflow menu opens from. */
export function OverflowMenuButton({
  label,
  busy = false,
  ...props
}: { label: string; busy?: boolean } & Omit<ComponentProps<typeof IconButton>, 'label' | 'children'>) {
  return (
    <IconButton label={label} variant="ghost" size="sm" {...props}>
      {busy ? <Spinner /> : <MoreVertical size={14} aria-hidden="true" />}
    </IconButton>
  )
}

/**
 * The three-dot menu on a row or a card. Destructive actions, wherever they
 * are listed, are drawn last under their own rule.
 */
export function OverflowMenu({
  label,
  actions,
  sections = [],
  busy = false,
  open,
  onOpenChange,
  anchorOnly = false,
  anchorPoint,
}: OverflowMenuProps) {
  const listed: readonly OverflowSection[] = [{ entries: actions }, ...sections]
  const blocks = listed
    .map((block) => ({
      label: block.label,
      entries: block.entries.filter(
        (entry): entry is OverflowAction | OverflowCheck | ReactElement =>
          Boolean(entry) && !isDanger(entry),
      ),
    }))
    .filter((block) => block.entries.length > 0)
  const danger = listed.flatMap((block) => block.entries).filter(isDanger)
  if (danger.length > 0) blocks.push({ label: undefined, entries: danger })

  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>
        {anchorOnly ? (
          <span
            className="row-menu__anchor"
            data-pointer={anchorPoint === undefined ? undefined : true}
            style={anchorPoint === undefined ? undefined : { left: anchorPoint.x, top: anchorPoint.y }}
            aria-hidden="true"
          />
        ) : (
          <OverflowMenuButton label={label} busy={busy} />
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align={anchorPoint !== undefined ? 'start' : anchorOnly ? 'center' : 'end'}
        sideOffset={anchorPoint !== undefined ? 0 : undefined}
      >
        {blocks.map((block, index) => (
          <Fragment key={index}>
            {index > 0 ? <DropdownMenuSeparator /> : null}
            {block.label ? <DropdownMenuLabel>{block.label}</DropdownMenuLabel> : null}
            {block.entries.map((entry, at) => (
              <OverflowItem key={at} entry={entry} />
            ))}
          </Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function isDanger(entry: OverflowEntry): entry is OverflowAction {
  if (!entry || isValidElement(entry) || 'checked' in entry) return false
  return entry.danger === true
}

function OverflowItem({ entry }: { entry: OverflowAction | OverflowCheck | ReactElement }) {
  if (isValidElement(entry)) return entry
  if ('checked' in entry) {
    return (
      <DropdownMenuCheckboxItem
        checked={entry.checked}
        onCheckedChange={(checked) => entry.onCheckedChange(checked === true)}
      >
        {entry.icon}
        {entry.label}
      </DropdownMenuCheckboxItem>
    )
  }
  if (entry.items) {
    return (
      <DropdownMenuSub>
        <DropdownMenuSubTrigger disabled={entry.disabled}>{entry.label}</DropdownMenuSubTrigger>
        <DropdownMenuSubContent>
          {entry.items.map((item, at) => (
            <OverflowItem key={at} entry={item} />
          ))}
        </DropdownMenuSubContent>
      </DropdownMenuSub>
    )
  }
  if (entry.href !== undefined || entry.to !== undefined) {
    const contents = (
      <>
        {entry.icon ? <span className="menu__item-indicator">{entry.icon}</span> : null}
        {entry.label}
      </>
    )
    return (
      <DropdownMenuItem asChild disabled={entry.disabled}>
        {entry.to !== undefined ? (
          <Link to={entry.to}>{contents}</Link>
        ) : (
          <a href={entry.href} target="_blank" rel="noreferrer noopener">
            {contents}
          </a>
        )}
      </DropdownMenuItem>
    )
  }
  return (
    <DropdownMenuItem
      icon={entry.icon}
      danger={entry.danger}
      disabled={entry.disabled}
      title={entry.title}
      onSelect={entry.onSelect}
    >
      {entry.label}
    </DropdownMenuItem>
  )
}
