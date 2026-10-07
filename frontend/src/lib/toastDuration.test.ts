import { describe, expect, it } from 'vitest'

import { asToastMs, toastDurationLabel } from './toastDuration'

describe('the toast duration', () => {
  it('defaults to six seconds', () => {
    expect(asToastMs(undefined)).toBe(6000)
    expect(asToastMs('nonsense')).toBe(6000)
  })

  it('keeps a value inside the bounds the API enforces and refuses one outside', () => {
    expect(asToastMs(12000)).toBe(12000)
    expect(asToastMs('4500')).toBe(4500)
    expect(asToastMs(0)).toBe(6000)
    expect(asToastMs(600000)).toBe(6000)
  })

  it('reads in seconds', () => {
    expect(toastDurationLabel(6000)).toBe('6 seconds')
    expect(toastDurationLabel(4500)).toBe('4.5 seconds')
  })
})
