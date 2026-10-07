import { describe, expect, it } from 'vitest'

import { describeRecipient, isSSHPrivateKey } from './backupKeys'

// Invented keys made by ssh-keygen for these tests; the same ed25519 key is in
// backend/internal/backup/crypt_test.go.
const ED25519 = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHtfBk58AasAD2LR2xeQ/qM6+2BhtSPUDwJP25s93+Bw'
const RSA_2048 =
  'ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCcrc24qm41BU6zw7B+CwVhKhw0nEaxmJbzy/Fd0zlNYj+wBBDlA1YU7qGy0NTJ' +
  'RrnD1XnvQWsjsVX18vL+XutUDo56yaykLsAQuwmK7HwpFpY2a4XxavsJbtb+Wx/ckWZn5r1wgbASzXwnCAzLKN/Myii3mOyagyQ' +
  'Gf1/odCS+CAPR25K8nyF9wNkrS5Q9pvm+iMRj7uC+X9fybRfm7/7bUuPc1Wy9pCBQ65rt2mwWiYvaxyvWo71nAgRyUfap1VCYck' +
  'PFSuu15DxDNYAM6Tj2OlUhhyPitdBSJibDS2B+c7bu1WZ6uvw4w/i3bOdJTIM+SCCpEWXswQQApGHnyY/n'
const RSA_1024 =
  'ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQC2zu810GSEFISTGi1Gwl2mmQFLxSspJmEPlO63OORvkMVwiQVBw3DXhoCigLvV' +
  '6Fr9iv9G3uzYByKtQI8q/x9CxV3dzQeLi2ZyYZERpp0sloqP2qQv9tzkXF2cv4dEe0/6fD/g/PPM7idb1ozXTQzRLe6FPhGMxUGv' +
  'Zi+U0j0mYQ=='
const ECDSA =
  'ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBMAMFI5n6JhBpnYJQN/7m/CqyDlJs' +
  'X1+ONlONL9O7KuSYG4LzRmH8IpwLSLgjxnujqO+QILRUx+8PHCEX22hKhE='

describe('a recipient', () => {
  it('is an age key', () => {
    expect(describeRecipient(' age1n946z2m9xyhkwem4vn9yj7hj7trelehnwv6q09vqqkzfq4sz3v8qp6rp0m ')).toEqual({
      kind: 'age',
      comment: '',
    })
  })

  it('is an SSH Ed25519 key, with its comment', () => {
    expect(describeRecipient(`${ED25519} backup test@example.invalid\n`)).toEqual({
      kind: 'ssh-ed25519',
      comment: 'backup test@example.invalid',
    })
    expect(describeRecipient(ED25519)).toEqual({ kind: 'ssh-ed25519', comment: '' })
  })

  it('is an SSH RSA key of 2048 bits, and not one of 1024', () => {
    expect(describeRecipient(`${RSA_2048} rsa-test@example.invalid`)).toEqual({
      kind: 'ssh-rsa',
      comment: 'rsa-test@example.invalid',
    })
    expect(describeRecipient(RSA_1024)).toBeNull()
  })

  it('is not an ECDSA key, a private key or a mangled one', () => {
    for (const bad of [
      ECDSA,
      'AGE-SECRET-KEY-15WVA2EEQZFV75G9LJENM2PVQS9M3WURETAGJXX4PEMUVLWW40V7SXLFC3W',
      'ssh-ed25519',
      'ssh-ed25519 AAAA',
      ED25519.slice(0, -4),
      ED25519.replace('ssh-ed25519', 'ssh-rsa'),
      '',
    ]) {
      expect(describeRecipient(bad), bad).toBeNull()
    }
  })
})

describe('an identity', () => {
  const armoured = (type: string, body: string) => `-----BEGIN ${type}-----${body}-----END ${type}-----`

  it('is an SSH private key when it has the armour of one', () => {
    expect(isSSHPrivateKey(armoured('OPENSSH PRIVATE KEY', '\nnot a key\n'))).toBe(true)
    expect(isSSHPrivateKey(armoured('RSA PRIVATE KEY', 'not a key'))).toBe(true)
    expect(isSSHPrivateKey(armoured('PUBLIC KEY', 'not a key'))).toBe(false)
    expect(isSSHPrivateKey('AGE-SECRET-KEY-15WVA2EEQZFV75G9LJENM2PVQS9M3WURETAGJXX4PEMUVLWW40V7SXLFC3W')).toBe(false)
  })
})
