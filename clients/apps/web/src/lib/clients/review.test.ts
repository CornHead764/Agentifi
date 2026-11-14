/**
 * A review row as the wire carries it. A row that looks right has no reason
 * on the wire, since proto3 leaves a zero enum out, so "looks right" is a null
 * reason and never the enum's unspecified name.
 */

import { create } from '@bufbuild/protobuf'
import { describe, expect, it } from 'vitest'

import { ReviewDaySchema, ReviewReason as WireReason } from '@/gen/agentifi/v1/review_pb'
import { fromWire } from '@/lib/rpc/wire'

import { looksRight, type ReviewDay } from './review'

function dayOf(reason: WireReason): ReviewDay {
  const wire = create(ReviewDaySchema, {
    date: '2026-06-14',
    out: { amount: '20.00' },
    rows: [{ transactionId: 'txn-1', amount: { amount: '-20.00' }, reason }],
  })
  return fromWire<ReviewDay>(ReviewDaySchema, wire)
}

describe('a review row from the wire', () => {
  it('looks right with no reason', () => {
    const [row] = dayOf(WireReason.UNSPECIFIED).rows
    expect(row.reason).toBeNull()
    expect(looksRight(row)).toBe(true)
  })

  it('needs a look with a reason', () => {
    const [row] = dayOf(WireReason.GUESS).rows
    expect(row.reason).toBe('guess')
    expect(looksRight(row)).toBe(false)
  })
})
