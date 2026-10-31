import { describe, expect, it } from 'vitest'

import {
  ACCEPTED_ATTACHMENTS,
  MAX_ATTACHMENT_BYTES,
  formatSize,
} from './attachments'

describe('the size a person reads', () => {
  it('counts bytes, then kilobytes, then megabytes', () => {
    expect(formatSize(512)).toBe('512 B')
    expect(formatSize(2048)).toBe('2 KB')
    expect(formatSize(1024 * 1024 * 2.5)).toBe('2.5 MB')
  })

  it('describes the limit the control enforces', () => {
    expect(formatSize(MAX_ATTACHMENT_BYTES)).toBe('25.0 MB')
  })
})

// The picker's filter must match the server's allowlist. SVG is absent from
// both because it can carry script.
describe('the accepted types', () => {
  it('offers exactly what the server stores', () => {
    expect(ACCEPTED_ATTACHMENTS.split(',')).toEqual([
      'image/jpeg',
      'image/png',
      'image/gif',
      'image/webp',
      'image/heic',
      'image/heif',
      'application/pdf',
    ])
  })

  it('does not offer SVG', () => {
    expect(ACCEPTED_ATTACHMENTS).not.toContain('svg')
  })
})
