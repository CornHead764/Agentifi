import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { MerchantOrderItem } from '@/lib/clients/merchant'

import { moneyFromCents } from '@/lib/money'

import { ItemList } from './ItemList'

const item = (over: Partial<MerchantOrderItem>): MerchantOrderItem => ({
  sku: '1',
  title: 'KS ORG EGGS',
  quantity: 1,
  unit_price: null,
  total_owed: null,
  shipped_on: null,
  condition: '',
  url: '',
  catalog: null,
  ...over,
})

describe('<ItemList>', () => {
  it('is one line per item, the product name when the catalog knows it, with a count', () => {
    const html = renderToStaticMarkup(
      <ItemList
        items={[
          item({ sku: '1234567', quantity: 2, catalog: { title: 'Kirkland Signature Organic Eggs, 24-count', brand: '', size: '', category: '', image_url: '', url: 'https://sameday.costco.com/store/costco/products/1' } }),
          item({ sku: '5', title: 'ROTISSERIE CHKN' }),
        ]}
      />,
    )
    expect(html).toMatch(/<ul class="merchant-items"><li[^>]*>2 × <a href="https:\/\/sameday\.costco\.com[^"]*"[^>]*>Kirkland Signature Organic Eggs, 24-count<\/a><\/li><li[^>]*>ROTISSERIE CHKN<\/li><\/ul>/)
    expect(html).not.toContain(' · ')
  })

  it('says how many more it left out, and that a file had none', () => {
    const many = Array.from({ length: 9 }, (_, i) => item({ sku: String(i), title: `Thing ${i}` }))
    const html = renderToStaticMarkup(<ItemList items={many} limit={3} />)
    expect(html).toContain('Thing 2')
    expect(html).not.toContain('Thing 3')
    expect(html).toContain('and 6 more')
    expect(renderToStaticMarkup(<ItemList items={[]} />)).toContain('No items in the file')
  })

  it('inside a button it is spans with list roles, never a <ul>', () => {
    const html = renderToStaticMarkup(<ItemList items={[item({})]} inline />)
    expect(html).toBe('<span class="merchant-items" role="list"><span role="listitem">KS ORG EGGS</span></span>')
  })

  it('dates each shipped item when asked, falling back to the order date', () => {
    const html = renderToStaticMarkup(
      <ItemList
        items={[item({ total_owed: moneyFromCents(2600), shipped_on: '2026-08-02' }), item({ sku: '2', title: 'LATER', total_owed: moneyFromCents(100) })]}
        orderedOn="2026-08-01"
        showDates
      />,
    )
    expect(html).toMatch(/KS ORG EGGS<span class="muted"> · [^<]*Aug[^<]*2<\/span>/)
    expect(html).toMatch(/LATER<span class="muted"> · [^<]*Aug[^<]*1<\/span>/)
  })
})
