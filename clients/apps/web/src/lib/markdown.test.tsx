import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { blocksOf, inlineNodes, renderMarkdown } from './markdown'

const render = (source: string) => renderToStaticMarkup(<>{renderMarkdown(source)}</>)

describe('the Markdown a model writes', () => {
  it('renders bold, italic and inline code as marks rather than asterisks', () => {
    expect(render('A gain of **642 900.00 US$**.')).toBe('<p>A gain of <strong>642 900.00 US$</strong>.</p>')
    expect(render('That is *roughly* right.')).toBe('<p>That is <em>roughly</em> right.</p>')
    expect(render('Read `/net-worth` for it.')).toBe('<p>Read <code>/net-worth</code> for it.</p>')
  })

  it('keeps an asterisk that pairs with nothing', () => {
    expect(render('2 * 3 = 6')).toBe('<p>2 * 3 = 6</p>')
    expect(render('A lone ** stays')).toBe('<p>A lone ** stays</p>')
  })

  it('does not read marks inside a code span', () => {
    expect(render('Try `a ** b` here.')).toBe('<p>Try <code>a ** b</code> here.</p>')
  })

  it('renders a fenced block as preformatted code, fences gone', () => {
    const html = render('Working:\n```\n412345.67 ÷ 84 = 4908.88\n```\nSo about that.')
    expect(html).toContain('<pre class="chat__code"><code>412345.67 ÷ 84 = 4908.88</code></pre>')
    expect(html).not.toContain('```')
    expect(html).toContain('<p>Working:</p>')
    expect(html).toContain('<p>So about that.</p>')
  })

  it('leaves the text inside a fence alone', () => {
    expect(render('```\n**not bold** and # not a heading\n```')).toBe(
      '<pre class="chat__code"><code>**not bold** and # not a heading</code></pre>',
    )
  })

  it('renders both kinds of list, and starts a new one when the kind changes', () => {
    expect(render('- Eggs\n- Chicken')).toBe('<ul><li>Eggs</li><li>Chicken</li></ul>')
    expect(render('1. First\n2. Second')).toBe('<ol><li>First</li><li>Second</li></ol>')
    const mixed = render('- Bullet\n1. Number')
    expect(mixed).toBe('<ul><li>Bullet</li></ul><ol><li>Number</li></ol>')
  })

  it('renders headings below the page-s own, so a reply cannot outrank the screen', () => {
    expect(render('## What changed')).toBe('<h4>What changed</h4>')
    expect(render('#### A detail')).toBe('<h5>A detail</h5>')
  })

  it('joins the lines of one paragraph and splits on a blank line', () => {
    expect(render('One line\nand its rest.\n\nA second.')).toBe(
      '<p>One line and its rest.</p><p>A second.</p>',
    )
  })

  it('prints plain text as the paragraph it already was', () => {
    expect(render('Just an answer.')).toBe('<p>Just an answer.</p>')
    expect(render('')).toBe('')
  })

  it('splits blocks on fences and nowhere else', () => {
    const blocks = blocksOf('a\n```js\ncode\n```\nb')
    expect(blocks.map((b) => b.kind)).toEqual(['text', 'code', 'text'])
    expect(blocks[1].language).toBe('js')
    expect(blocks[1].lines).toEqual(['code'])
  })

  it('returns inline nodes a caller can key', () => {
    const nodes = inlineNodes('**a** b')
    expect(nodes).toHaveLength(2)
  })
})

describe('links in what a model writes', () => {
  const routed = (source: string) =>
    renderToStaticMarkup(<MemoryRouter>{renderMarkdown(source)}</MemoryRouter>)

  it('makes a link into this app a link', () => {
    const html = routed('Give them this: [Sign in to Amazon](/settings/merchants/amazon?sign-in=a1).')
    expect(html).toMatch(/^<p>Give them this: <a href="\/settings\/merchants\/amazon\?sign-in=a1"[^>]*>Sign in to Amazon<\/a>\.<\/p>$/)
  })

  it('leaves a link anywhere else as the text it was', () => {
    expect(routed('[the bank](https://bank.example/login)')).toBe(
      '<p>[the bank](https://bank.example/login)</p>',
    )
    expect(routed('[elsewhere](//bank.example/login)')).toBe('<p>[elsewhere](//bank.example/login)</p>')
    expect(routed('[odd](javascript:alert(1))')).toBe('<p>[odd](javascript:alert(1))</p>')
  })
})
