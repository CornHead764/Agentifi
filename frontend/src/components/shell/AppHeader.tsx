import {
  Bell,
  ChevronDown,
  CircleHelp,
  Landmark,
  Eye,
  EyeOff,
  Inbox,
  LogOut,
  Monitor,
  Moon,
  Settings,
  Sun,
  User,
  Users,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { SetupGuideLink } from '@/components/onboarding/SetupGuide'
import { ShortcutSheet } from '@/components/transactions/ShortcutSheet'
import { useAuth } from '@/contexts/auth'
import { usePrivacy } from '@/contexts/privacy'
import { KEY_SHORTCUTS, SEARCH_SHORTCUTS } from '@/lib/transactions/shortcuts'
import { useTheme, type ThemePreference } from '@/contexts/theme'
import {
  Button,
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  EmptyState,
  IconButton,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Switch,
  Tooltip,
} from '@/components/ui'

import { NotificationItem, type ShellNotification } from './NotificationItem'

export type { ShellNotification }

export interface AppHeaderProps {
  title: string
  notifications?: readonly ShellNotification[]
  onMarkAllRead?: () => void
  /** Takes every card off the feed, read or not. */
  onClearAll?: () => void
  /** Called when a card is opened, so it can be marked read. */
  onOpenNotification?: (id: string) => void
  onClearNotification?: (id: string) => void
  /** Initials for the avatar; falls back to a generic mark. */
  initials?: string
  /** The active space's name, shown only when there is more than one. */
  space?: string
  /** Every space this person is in, for switching from the name itself. */
  spaces?: readonly { id: string; name: string }[]
  activeSpaceId?: string
  onSwitchSpace?: (id: string) => void
  /**
   * The server's whole unread count. The cards are only the first page of
   * the feed, so counting their flags under-reports past a page.
   */
  unreadCount?: number
  /** The bell's filter, present only when a caller can change what the feed asks for. */
  unreadOnly?: boolean
  onUnreadOnlyChange?: (unreadOnly: boolean) => void
  /** The accounts drawer's only toggle, on every page that carries the drawer. */
  accounts?: { open: boolean; onToggle: () => void }
  /**
   * Drawn in place of the whole header row by a page in a mode of its own. It
   * keeps the header's box, so what sticks under it (`--header-height`) stays.
   */
  override?: ReactNode
}

export function AppHeader({
  title,
  notifications,
  onMarkAllRead,
  onClearAll,
  onOpenNotification,
  onClearNotification,
  initials,
  space,
  spaces,
  activeSpaceId,
  onSwitchSpace,
  unreadCount,
  unreadOnly = false,
  onUnreadOnlyChange,
  accounts,
  override,
}: AppHeaderProps) {
  const unread =
    unreadCount ?? notifications?.filter((notification) => notification.unread).length ?? 0

  // Replaces the row entirely, so the bell and avatar are not still tabbable
  // behind it.
  if (override) return <header className="header">{override}</header>

  return (
    <header className="header">
      {accounts ? (
        <IconButton
          label={accounts.open ? 'Hide accounts' : 'Show accounts'}
          variant="ghost"
          className="header__accounts"
          aria-expanded={accounts.open}
          onClick={accounts.onToggle}
        >
          <Landmark size={16} />
        </IconButton>
      ) : null}
      <h1 className="header__title">{title}</h1>
      {space && spaces && onSwitchSpace ? (
        <DropdownMenu>
          <Tooltip label="Switch space" side="bottom">
            <DropdownMenuTrigger asChild>
              <button type="button" className="header__space" aria-label={`Space: ${space}. Switch space`}>
                <Users size={14} aria-hidden="true" className="header__space-icon" />
                <span className="header__space-name">{space}</span>
                <ChevronDown size={12} aria-hidden="true" className="header__space-chevron" />
              </button>
            </DropdownMenuTrigger>
          </Tooltip>
          <DropdownMenuContent align="start">
            <DropdownMenuLabel>Spaces</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={activeSpaceId ?? ''} onValueChange={onSwitchSpace}>
              {spaces.map((one) => (
                <DropdownMenuRadioItem key={one.id} value={one.id}>
                  {one.name}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <Link to="/settings/spaces">Manage spaces</Link>
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ) : space ? (
        <Link to="/settings/spaces" className="header__space" aria-label={`Space: ${space}`}>
          <Users size={14} aria-hidden="true" className="header__space-icon" />
          <span className="header__space-name">{space}</span>
        </Link>
      ) : null}
      <div className="header__spacer" />

      <div className="row row--1 header__actions">
        <Popover>
          <Tooltip label="Notifications" side="bottom">
            <PopoverTrigger asChild>
              <IconButton
                label={unread ? `Notifications, ${unread} unread` : 'Notifications'}
                variant="ghost"
                className="header__bell"
              >
                <Bell size={16} />
                {/* Capped at the badge; the full count is in the button's label. */}
                {unread > 0 ? (
                  <span className="header__badge">{unread > 99 ? '99+' : unread}</span>
                ) : null}
              </IconButton>
            </PopoverTrigger>
          </Tooltip>
          <PopoverContent flush className="notifications" aria-label="Notifications">
            <div className="notifications__header">
              <strong>Notifications</strong>
              {onUnreadOnlyChange ? (
                <Switch
                  checked={unreadOnly}
                  onCheckedChange={onUnreadOnlyChange}
                  label="Unread only"
                  labelPosition="before"
                />
              ) : null}
              <Button
                variant="ghost"
                size="sm"
                onClick={onMarkAllRead}
                disabled={unread === 0}
              >
                Mark all as read
              </Button>
            </div>
            {notifications && notifications.length > 0 ? (
              <ul className="notifications__list">
                {notifications.map((notification) => (
                  <NotificationItem
                    key={notification.id}
                    notification={notification}
                    onOpen={onOpenNotification}
                    onClear={onClearNotification}
                  />
                ))}
              </ul>
            ) : (
              <EmptyState
                icon={<Inbox size={20} />}
                title={unreadOnly ? 'Nothing unread' : 'No notifications'}
              />
            )}
            <div className="notifications__footer">
              {onClearAll ? (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={onClearAll}
                  disabled={!notifications || notifications.length === 0}
                >
                  Clear all
                </Button>
              ) : null}
              <Link to="/settings/notifications">Notification settings</Link>
            </div>
          </PopoverContent>
        </Popover>

        <Popover>
          <Tooltip label="Help" side="bottom">
            <PopoverTrigger asChild>
              <IconButton label="Help" variant="ghost" className="header__help">
                <CircleHelp size={16} />
              </IconButton>
            </PopoverTrigger>
          </Tooltip>
          <PopoverContent className="shortcuts">
            <SetupGuideLink />
            <ShortcutSheet groups={KEY_SHORTCUTS} />
            <ShortcutSheet
              groups={SEARCH_SHORTCUTS}
              lead={{
                heading: 'Search language',
                body: (
                  <>
                    Space-separated terms narrow together, e.g.{' '}
                    <code>date=this-month amount&gt;500</code>
                  </>
                ),
              }}
            />
          </PopoverContent>
        </Popover>

        {/* The one way into Settings from the header. */}
        <Tooltip label="Settings" side="bottom">
          <IconButton label="Settings" variant="ghost" asChild>
            <Link to="/settings/general">
              <Settings size={16} />
            </Link>
          </IconButton>
        </Tooltip>

        <AccountMenu initials={initials} />
      </div>
    </header>
  )
}

/**
 * This account, and the preferences that belong to it. Theme and privacy mode
 * are also here because they are switched often.
 */
function AccountMenu({ initials }: { initials?: string }) {
  const { preference, setPreference } = useTheme()
  const { hidden, setHidden } = usePrivacy()
  const { user, signOut } = useAuth()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" className="header__avatar" aria-label="Account and preferences">
          {initials ?? <User size={16} />}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        {user ? <DropdownMenuLabel>{user.email}</DropdownMenuLabel> : null}
        <DropdownMenuLabel>Appearance</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={preference}
          onValueChange={(value) => setPreference(asThemePreference(value))}
        >
          <DropdownMenuRadioItem value="light">
            <Sun size={14} /> Light
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">
            <Moon size={14} /> Dark
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="system">
            <Monitor size={14} /> System
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>

        <DropdownMenuSeparator />

        <DropdownMenuCheckboxItem
          checked={hidden}
          onCheckedChange={(checked) => setHidden(checked === true)}
        >
          {hidden ? <EyeOff size={14} /> : <Eye size={14} />} Privacy mode
        </DropdownMenuCheckboxItem>

        <DropdownMenuSeparator />

        <DropdownMenuItem onSelect={() => void signOut()}>
          <LogOut size={14} /> Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * Narrows Radix's bare string, so an unknown preference is never written to
 * storage and read back for good.
 */
function asThemePreference(value: string): ThemePreference {
  return value === 'light' || value === 'system' ? value : 'dark'
}
