/**
 * The files behind a row: what a person attached, and the receipts filed on
 * it from its bill and its matched order, each marked as whose it is. Every
 * file and figure is invented.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'

import { SETTLED_BILLS_KEY, type SettledBill } from '@/lib/clients/bills'
import type { Document } from '@/lib/clients/documents'
import { parseMoney } from '@/lib/money'
import type { ReceiptStatus, Uuid } from '@/lib/transactions/types'
import { renderScreen } from '@/test/renderScreen'

import { ATTACHMENTS_KEY, AttachmentPanel } from './AttachmentPanel'

const ROW = 'txn-1' as Uuid

// A static render reads no media query, so the pointer is set here: coarse is
// a touch screen, which is where a camera is to hand.
const pointer = vi.hoisted(() => ({ coarse: false }))
vi.mock('@/lib/useMediaQuery', () => ({
  useMediaQuery: (query: string) => query === '(pointer: coarse)' && pointer.coarse,
}))

afterEach(() => {
  pointer.coarse = false
})

function doc(over: Partial<Document>): Document {
  return {
    id: 'doc-1',
    filename: 'file.pdf',
    content_type: 'application/pdf',
    size_bytes: 2048,
    url: '/documents/doc-1/content',
    source: 'upload',
    source_ref: '',
    uploaded_by_user_id: null,
    created_at: '2026-09-16T12:00:00Z',
    ...over,
  }
}

function render(
  documents: Document[],
  settled: SettledBill[] = [],
  receiptStatus: ReceiptStatus | null = null,
): string {
  return renderScreen(<AttachmentPanel transactionId={ROW} receiptStatus={receiptStatus} />, {
    seed: [
      [ATTACHMENTS_KEY(ROW), documents],
      [SETTLED_BILLS_KEY(ROW), settled],
    ],
  })
}

describe('the files behind a row', () => {
  const statement = doc({
    id: 'doc-bill',
    filename: 'statement-2026-09.pdf',
    source: 'bill_pull',
    via: 'receipt',
    receipt_of: { kind: 'bill', name: 'Riverside Power', due_on: '2026-09-03' },
  })
  const invoice = doc({
    id: 'doc-order',
    filename: 'amazon-invoice-111-0000000-7171717.pdf',
    source: 'merchant_pull',
    via: 'receipt',
    receipt_of: { kind: 'merchant_order', name: 'Amazon', order_number: '111-0000000-7171717' },
  })
  const photo = doc({ id: 'doc-mine', filename: 'till-slip.jpg', via: 'transaction' })

  it('marks each receipt as coming from the bill or from the order', () => {
    const rendered = render([statement, invoice, photo])
    expect(rendered).toContain('From the bill')
    expect(rendered).toContain('Statement from Riverside Power, due Sep 3, 2026')
    expect(rendered).toContain('From the order')
    expect(rendered).toContain('Amazon order 111-0000000-7171717 invoice')
    expect(rendered.split('class="badge ').length - 1).toBe(2)
  })

  it('offers Remove only for what a person attached', () => {
    const rendered = render([statement, invoice, photo])
    expect(rendered).toContain('Remove till-slip.jpg')
    expect(rendered).not.toContain('Remove statement-2026-09.pdf')
    expect(rendered).not.toContain('Remove amazon-invoice-111-0000000-7171717.pdf')
  })

  it('opens a file rather than downloading it, and offers Download beside it', () => {
    const rendered = render([statement, photo])
    // An href would be followed without the bearer token, and `download`
    // would save the file rather than show it.
    expect(rendered).not.toContain('href=')
    expect(rendered).not.toContain('download=')
    expect(rendered).toContain('aria-label="Open statement-2026-09.pdf"')
    expect(rendered).toContain('aria-label="Open till-slip.jpg"')
    expect(rendered).toContain('aria-label="Download statement-2026-09.pdf"')
    expect(rendered).toContain('aria-label="Download till-slip.jpg"')
  })

  it('names the bill a payment settled when no statement came with it', () => {
    const bill: SettledBill = {
      bill_id: 'bill-1',
      connection_id: 'conn-1',
      provider: 'Riverside Life (policy)',
      due_on: '2026-01-16',
      amount_due: parseMoney('480.00'),
      status: 'paid',
      document_id: null,
    }
    const rendered = render([], [bill, { ...bill, bill_id: 'bill-2', document_id: 'doc-bill' }])
    expect(rendered).toContain('Pays the Riverside Life (policy) bill due Jan 16, 2026')
    expect(rendered).toContain('No statement document came with it.')
    expect(rendered.split('Pays the').length - 1).toBe(1)
  })
})

describe('taking a receipt', () => {
  it('offers the camera on a touch screen, beside the file picker, for the same upload', () => {
    pointer.coarse = true
    const rendered = render([])
    expect(rendered).toContain('Add a receipt')
    expect(rendered).toContain('Take photo')
    expect(rendered).toContain('capture="environment"')
    expect(rendered).toContain('accept="image/*"')
    expect(rendered.split('type="file"').length - 1).toBe(2)
  })

  it('offers only the file picker where there is no touch screen', () => {
    const rendered = render([])
    expect(rendered).toContain('Add a receipt')
    expect(rendered).not.toContain('Take photo')
    expect(rendered).not.toContain('capture=')
    expect(rendered.split('type="file"').length - 1).toBe(1)
  })

  it('says a receipt is owed when the account requires one and nothing is behind the row', () => {
    expect(render([], [], 'missing')).toContain('needs a receipt for every purchase')
  })

  it('says nothing is owed once a file is behind the row, or when none is needed', () => {
    const photo = doc({ id: 'doc-mine', filename: 'pharmacy.jpg', via: 'transaction' })
    expect(render([photo], [], 'missing')).not.toContain('needs a receipt')
    expect(render([], [], 'not_needed')).not.toContain('needs a receipt')
    expect(render([], [], null)).not.toContain('needs a receipt')
  })
})
