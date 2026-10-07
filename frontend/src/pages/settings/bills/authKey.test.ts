import { describe, expect, it } from 'vitest'

import { authKeyProblem, normalizeAuthKey } from './authKey'

// The key below is invented; this repository holds no real one.
const key = 'JBSWY3DPEHPK3PXP'

describe('the authenticator setup key', () => {
  it('is taken as the provider prints it', () => {
    expect(normalizeAuthKey('jbsw y3dp ehpk 3pxp')).toBe(key)
    expect(authKeyProblem('jbsw y3dp ehpk 3pxp')).toBeNull()
    expect(authKeyProblem(`${key}====`)).toBeNull()
  })

  it('says nothing about an empty field, which is the ordinary case', () => {
    expect(authKeyProblem('')).toBeNull()
    expect(authKeyProblem('   ')).toBeNull()
  })

  it('names the mistake that would only be found at four in the morning', () => {
    expect(authKeyProblem('182931')).toMatch(/code from the app/)
    expect(authKeyProblem('not a key!')).toMatch(/A–Z and digits 2–7/)
    expect(authKeyProblem('JBSW1')).toMatch(/A–Z and digits 2–7/)
  })
})
