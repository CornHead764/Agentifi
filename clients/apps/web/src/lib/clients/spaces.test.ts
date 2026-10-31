import { describe, expect, it } from 'vitest'

import {
  assignableRoles,
  canDeleteSpace,
  canManage,
  confirmsSpaceName,
  dayOf,
  isLastOwner,
  isPending,
  sortMembers,
  type Member,
  type Role,
  type Space,
} from './spaces'

function member(id: string, role: Role, accepted: boolean, name = id): Member {
  return {
    id,
    user_id: `u-${id}`,
    email: `${id}@example.test`,
    full_name: name,
    role,
    invited_at: '2026-08-01T09:00:00Z',
    accepted_at: accepted ? '2026-08-02T09:00:00Z' : null,
  }
}

function space(role: Role): Space {
  return {
    id: 's1',
    name: 'Household',
    primary_currency: 'USD',
    timezone: 'UTC',
    default_date_range: '',
    sidebar_account_types: null,
    role,
    can_write: true,
    is_owner: role === 'owner' || role === 'admin',
    joined_at: '2026-01-02T09:00:00Z',
  }
}

describe('a pending invitation', () => {
  it('is not somebody who is in the space', () => {
    expect(isPending(member('a', 'member', false))).toBe(true)
    expect(isPending(member('a', 'member', true))).toBe(false)
  })

  it('is listed after everyone who accepted', () => {
    const rows = sortMembers([
      member('zoe', 'member', false, 'Zoe'),
      member('ada', 'owner', true, 'Ada'),
      member('bob', 'viewer', true, 'Bob'),
    ])
    expect(rows.map((one) => one.full_name)).toEqual(['Ada', 'Bob', 'Zoe'])
  })
})

describe('the last owner', () => {
  it('is the only accepted membership that can administer the space', () => {
    const owner = member('ada', 'owner', true)
    const viewer = member('bob', 'viewer', true)
    expect(isLastOwner([owner, viewer], owner)).toBe(true)
    expect(isLastOwner([owner, viewer], viewer)).toBe(false)
  })

  it('is not the last one once somebody else has accepted as an owner', () => {
    const owner = member('ada', 'owner', true)
    const second = member('cid', 'owner', true)
    expect(isLastOwner([owner, second], owner)).toBe(false)
  })

  it('is not succeeded by an admin, the way the server counts it', () => {
    // An admin manages the people in a space but cannot make another owner,
    // so a space left with only admins could never be shared again.
    const owner = member('ada', 'owner', true)
    const admin = member('cid', 'admin', true)
    expect(isLastOwner([owner, admin], owner)).toBe(true)
    expect(isLastOwner([owner, admin], admin)).toBe(false)
  })

  it('is still the last one while the successor has only been invited', () => {
    // An invitation grants nothing: an owner who has not accepted cannot let
    // anybody in, so demoting the one who has would strand the space.
    const owner = member('ada', 'owner', true)
    const invited = member('cid', 'owner', false)
    expect(isLastOwner([owner, invited], owner)).toBe(true)
  })
})

describe('membership dates', () => {
  it('reads the day out of a timestamp', () => {
    expect(dayOf('2026-08-21T12:15:00Z')).toBe('Aug 21, 2026')
    expect(dayOf(null)).toBeNull()
  })
})

describe('what an admin may change', () => {
  const owner = member('ada', 'owner', true)
  const viewer = member('bob', 'viewer', true)

  it('is everybody but the owner', () => {
    // An admin changing an owner's membership gets a 403.
    expect(canManage(space('admin'), viewer)).toBe(true)
    expect(canManage(space('admin'), owner)).toBe(false)
    expect(canManage(space('owner'), owner)).toBe(true)
  })

  it('is nothing at all for a member or a viewer', () => {
    expect(canManage(space('member'), viewer)).toBe(false)
    expect(canManage(space('viewer'), viewer)).toBe(false)
  })

  it('never includes handing out the owner role', () => {
    expect(assignableRoles(space('owner'))).toContain('owner')
    expect(assignableRoles(space('admin'))).not.toContain('owner')
    expect(assignableRoles(space('admin'))).toEqual(['admin', 'member', 'viewer'])
  })
})

describe('deleting a space', () => {
  it('is an owner\'s act alone, never an admin\'s', () => {
    expect(canDeleteSpace(space('owner'), 2)).toBe(true)
    for (const role of ['admin', 'member', 'viewer'] as const) {
      expect(canDeleteSpace(space(role), 2)).toBe(false)
    }
  })

  it('leaves the caller at least one space', () => {
    expect(canDeleteSpace(space('owner'), 1)).toBe(false)
  })

  it('takes the name exactly, forgiving spaces at either end', () => {
    expect(confirmsSpaceName(' Household ', 'Household')).toBe(true)
    expect(confirmsSpaceName('household', 'Household')).toBe(false)
    expect(confirmsSpaceName('', 'Household')).toBe(false)
  })
})
