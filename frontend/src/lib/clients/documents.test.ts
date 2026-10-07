import { afterEach, describe, expect, it, vi } from 'vitest'

import { adoptUser, resetActiveSpace, setActiveSpace } from '@/lib/activeSpace'
import { setAccessToken } from '@/lib/session'

import {
  describeOrigin,
  documentContentUrl,
  fetchDocumentContent,
  isPreviewableDocument,
  isRemovableFromRow,
  receiptBadge,
  uploadDocument,
  type Document,
} from './documents'

function document(overrides: Partial<Document> = {}): Document {
  return {
    id: 'doc-1',
    filename: 'statement.pdf',
    content_type: 'application/pdf',
    size_bytes: 2048,
    url: '/documents/doc-1/content',
    source: 'upload',
    source_ref: '',
    uploaded_by_user_id: 'user-1',
    created_at: '2026-09-16T12:00:00Z',
    ...overrides,
  }
}

// What the file is to the row comes before where it was fetched from.
describe('where a file came from', () => {
  it('names what the file is to the row before naming the fetch', () => {
    expect(
      describeOrigin(
        document({
          via: 'receipt',
          source: 'bill_pull',
          receipt_of: { kind: 'bill', name: 'Riverside Power', due_on: '2026-09-03' },
        }),
      ),
    ).toBe('Statement from Riverside Power, due Sep 3, 2026')
    expect(
      describeOrigin(
        document({
          via: 'receipt',
          source: 'merchant_pull',
          receipt_of: { kind: 'merchant_order', name: 'Costco', order_number: '21100123456789012345' },
        }),
      ),
    ).toBe('Costco order 21100123456789012345 invoice')
  })

  it('still says whose paperwork it is when the server names less', () => {
    expect(describeOrigin(document({ via: 'receipt', receipt_of: { kind: 'bill', name: '' } }))).toBe(
      "The bill's statement",
    )
    expect(
      describeOrigin(document({ via: 'receipt', receipt_of: { kind: 'merchant_order', name: 'Amazon' } })),
    ).toBe('Amazon order invoice')
  })

  it("names the receipt a person also attached by hand", () => {
    expect(
      describeOrigin(
        document({
          via: 'transaction',
          receipt_of: { kind: 'merchant_order', name: 'Amazon', order_number: '111-0000000-7171717' },
        }),
      ),
    ).toBe('Amazon order 111-0000000-7171717 invoice')
  })

  it('falls back to how it arrived for a file on the row itself', () => {
    expect(describeOrigin(document({ via: 'transaction', source: 'upload' }))).toBe('Uploaded')
    expect(describeOrigin(document({ via: 'transaction', source: 'bill_pull' }))).toBe(
      'Pulled from the provider',
    )
    expect(describeOrigin(document({ source: 'email' }))).toBe('From an email')
    expect(describeOrigin(document({ source: 'receipt_scan' }))).toBe('Scanned receipt')
  })
})

// Remove is offered only for what the row holds, never a bill's statement.
describe('what this row may remove', () => {
  it('offers removal for what is attached to the row', () => {
    expect(isRemovableFromRow(document({ via: 'transaction' }))).toBe(true)
    expect(isRemovableFromRow(document())).toBe(true)
  })

  it('does not offer removal for a receipt filed on the row', () => {
    expect(
      isRemovableFromRow(document({ via: 'receipt', receipt_of: { kind: 'bill', name: 'Riverside Power' } })),
    ).toBe(false)
    expect(
      isRemovableFromRow(document({ via: 'receipt', receipt_of: { kind: 'merchant_order', name: 'Amazon' } })),
    ).toBe(false)
  })

  it('keeps Remove on a hand attachment that is also the receipt', () => {
    expect(
      isRemovableFromRow(document({ via: 'transaction', receipt_of: { kind: 'bill', name: 'Riverside Power' } })),
    ).toBe(true)
  })
})

describe('the mark on a receipt', () => {
  it('says whether it came from the bill or the order', () => {
    expect(receiptBadge(document({ via: 'receipt', receipt_of: { kind: 'bill', name: 'x' } }))).toBe(
      'From the bill',
    )
    expect(
      receiptBadge(document({ via: 'receipt', receipt_of: { kind: 'merchant_order', name: 'Amazon' } })),
    ).toBe('From the order')
    expect(receiptBadge(document({ via: 'transaction' }))).toBeNull()
  })
})

describe('what the panel can preview', () => {
  it('renders an image inline and gives a PDF the file icon', () => {
    expect(isPreviewableDocument(document({ content_type: 'image/png' }))).toBe(true)
    expect(isPreviewableDocument(document({ content_type: 'application/pdf' }))).toBe(false)
  })
})

describe('the content path', () => {
  it('matches the url the server returns for the same document', () => {
    expect(documentContentUrl('doc-1')).toBe(document().url)
  })
})

/** The content endpoint needs the bearer token; there is no session cookie. */
describe('fetching a document by id', () => {
  function stubFetch() {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: { get: () => null },
      blob: () => Promise.resolve(new Blob(['%PDF-'])),
    })
    vi.stubGlobal('fetch', fetcher)
    return fetcher
  }

  function headersOf(fetcher: ReturnType<typeof stubFetch>): Record<string, string> {
    const init: unknown = fetcher.mock.calls[0]?.[1]
    return (init as { headers: Record<string, string> }).headers
  }

  afterEach(() => {
    setAccessToken(null)
    resetActiveSpace()
    vi.unstubAllGlobals()
  })

  it('asks the API base for the content path, not the frontend origin', async () => {
    const fetcher = stubFetch()

    await fetchDocumentContent('doc-1')

    expect(fetcher.mock.calls[0]?.[0]).toBe('/api/documents/doc-1/content')
  })

  it('carries the bearer token and the chosen space', async () => {
    const fetcher = stubFetch()
    adoptUser('u-ada')
    setAccessToken('tok-abc')
    setActiveSpace('s-cabin')

    await fetchDocumentContent('doc-1')

    expect(headersOf(fetcher).Authorization).toBe('Bearer tok-abc')
    expect(headersOf(fetcher)['X-Space-Id']).toBe('s-cabin')
  })
})

describe('uploading a document', () => {
  function stubFetch() {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      headers: { get: () => null },
      text: () => Promise.resolve(JSON.stringify(document())),
    })
    vi.stubGlobal('fetch', fetcher)
    return fetcher
  }

  function sent(fetcher: ReturnType<typeof stubFetch>): { path: unknown; form: FormData } {
    const init = fetcher.mock.calls[0]?.[1] as { body: FormData }
    return { path: fetcher.mock.calls[0]?.[0], form: init.body }
  }

  afterEach(() => vi.unstubAllGlobals())

  it('posts the file and its link to the one upload route', async () => {
    const fetcher = stubFetch()
    const file = new File(['%PDF-'], 'receipt.pdf', { type: 'application/pdf' })

    await uploadDocument({ kind: 'transaction', target_id: 'txn-1' }, file)

    const { path, form } = sent(fetcher)
    expect(path).toBe('/api/documents')
    expect(form.get('kind')).toBe('transaction')
    expect(form.get('target_id')).toBe('txn-1')
    expect(form.get('role')).toBeNull()
    expect((form.get('file') as File).name).toBe('receipt.pdf')
  })

  it("sends a bill's statement role", async () => {
    const fetcher = stubFetch()
    const file = new File(['%PDF-'], 'statement.pdf', { type: 'application/pdf' })

    await uploadDocument({ kind: 'bill', target_id: 'bill-1', role: 'statement' }, file)

    expect(sent(fetcher).form.get('role')).toBe('statement')
  })
})
