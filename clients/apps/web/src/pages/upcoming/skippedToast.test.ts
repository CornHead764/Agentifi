import { describe, expect, it } from 'vitest'

import { runInOrder } from '@/components/plan/bulkEnvelope'
import type { Occurrence } from '@/lib/clients/upcoming'

import { skippedToast } from './skippedToast'

function occurrence(label: string, due_on: string): Occurrence {
  return { series_id: label, label, due_on } as unknown as Occurrence
}

const WATER = occurrence('Water', '2026-09-01')
const GYM = occurrence('Gym', '2026-09-05')
const PHONE = occurrence('Phone', '2026-09-10')

describe('skippedToast', () => {
  it('keeps going past a refused skip and says how many of how many went through', async () => {
    const skipped: string[] = []
    const result = await runInOrder([WATER, GYM, PHONE], async (one) => {
      if (one === GYM) throw new Error('refused')
      skipped.push(one.label)
    })
    expect(skipped).toEqual(['Water', 'Phone'])

    const toast = skippedToast(result)
    expect(toast.tone).toBe('error')
    expect(toast.title).toBe('Skipped 2 of 3 reminders')
    expect(toast.description).toContain('Gym (2026-09-05)')
  })

  it('counts a clean run', async () => {
    const result = await runInOrder([WATER, PHONE], async () => {})
    expect(skippedToast(result)).toMatchObject({ title: 'Skipped 2 reminders', tone: 'success' })
  })
})
