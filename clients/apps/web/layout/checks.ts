export type CheckName =
  | 'page-scroll'
  | 'card-overflow'
  | 'row-heights'
  | 'control-heights'
  | 'control-styles'
  | 'header-actions'
  | 'header-title'
  | 'overlap'
  | 'alignment'
  | 'console'
  | 'fixtures'

export interface Violation {
  check: CheckName
  /** Which element, as a reader finds it on the screenshot. */
  element: string
  detail: string
}

/**
 * The layout checks, run inside the page. Passed to `page.evaluate`, so it is
 * serialized on its own: everything it uses is declared inside it.
 */
export function measureLayout(): Violation[] {
  const TOLERANCE = 1
  const out: Violation[] = []

  const rect = (el: Element) => el.getBoundingClientRect()

  const visible = (el: Element): boolean => {
    if (el.closest('[inert], [aria-hidden="true"]')) return false
    const style = getComputedStyle(el)
    if (style.visibility === 'hidden' || style.display === 'none') return false
    const box = rect(el)
    return box.width > 0 && box.height > 0
  }

  /** Visually hidden text (a 1px clipped box) is not layout. */
  const screenReaderOnly = (el: Element): boolean => {
    const box = rect(el)
    const style = getComputedStyle(el)
    return (box.width <= 1 && box.height <= 1) || (style.clip !== 'auto' && style.clip !== '')
  }

  const text = (el: Element, max = 40): string => {
    const label = el.getAttribute('aria-label') ?? el.getAttribute('placeholder')
    const raw = (label ?? el.textContent ?? '').replace(/\s+/g, ' ').trim()
    return raw.length > max ? `${raw.slice(0, max - 1)}…` : raw
  }

  const classes = (el: Element): string =>
    Array.from(el.classList)
      .slice(0, 2)
      .map((name) => `.${name}`)
      .join('')

  /** Where on the page: the nearest card's or page header's title. */
  const place = (el: Element): string => {
    const card = el.closest('.card')
    const title = card?.querySelector(':scope .card__title, :scope .page-header__title')
    if (title) return ` in card "${text(title)}"`
    if (card) return ` in card ${classes(card) || ''}`.trimEnd()
    return ''
  }

  const describe = (el: Element): string => {
    const label = text(el)
    return `${el.tagName.toLowerCase()}${classes(el)}${label ? ` "${label}"` : ''}${place(el)}`
  }

  const scrolls = (el: Element): boolean => {
    const style = getComputedStyle(el)
    return ['auto', 'scroll', 'hidden', 'clip'].includes(style.overflowX)
  }

  // 1. No horizontal scroll on the page.
  const root = document.scrollingElement ?? document.documentElement
  if (root.scrollWidth > root.clientWidth + TOLERANCE) {
    const culprits: string[] = []
    const walk = (el: Element) => {
      if (culprits.length >= 3 || !visible(el)) return
      if (getComputedStyle(el).position === 'fixed') return
      if (rect(el).right > root.clientWidth + TOLERANCE && !screenReaderOnly(el)) {
        const deeper = Array.from(el.children).some(
          (child) => visible(child) && rect(child).right > root.clientWidth + TOLERANCE,
        )
        if (!deeper || scrolls(el)) {
          culprits.push(`${describe(el)} ends at ${Math.round(rect(el).right)}px`)
          return
        }
      }
      if (scrolls(el) && el !== document.body) return
      for (const child of Array.from(el.children)) walk(child)
    }
    walk(document.body)
    out.push({
      check: 'page-scroll',
      element: 'page',
      detail: `scrollWidth ${root.scrollWidth}px > clientWidth ${root.clientWidth}px; widest: ${culprits.join('; ') || 'not found'}`,
    })
  }

  // 2. Nothing sticks out of a card. A scroll or clip container inside the
  // card is checked itself and not below it.
  for (const card of Array.from(document.querySelectorAll('.card'))) {
    if (!visible(card)) continue
    const edge = rect(card)
    const walk = (el: Element) => {
      if (!visible(el) || getComputedStyle(el).position === 'fixed') return
      const box = rect(el)
      if (
        !screenReaderOnly(el) &&
        (box.right > edge.right + TOLERANCE || box.left < edge.left - TOLERANCE)
      ) {
        const over = Math.round(Math.max(box.right - edge.right, edge.left - box.left))
        out.push({
          check: 'card-overflow',
          element: describe(el),
          detail: `overflows its card by ${over}px (card ${Math.round(edge.left)}–${Math.round(edge.right)}, element ${Math.round(box.left)}–${Math.round(box.right)})`,
        })
        return
      }
      if (scrolls(el) || el instanceof SVGElement) return
      for (const child of Array.from(el.children)) walk(child)
    }
    for (const child of Array.from(card.children)) walk(child)
  }

  // 3. Rows of one kind in one table or list are one height. A table stacked
  // into blocks on a phone, and a list row allowed to wrap, are exempt.
  const unevenRows = (container: Element, rows: Element[], kind: (row: Element) => string) => {
    const groups = new Map<string, Element[]>()
    for (const row of rows) {
      if (!visible(row)) continue
      const key = kind(row)
      groups.set(key, [...(groups.get(key) ?? []), row])
    }
    for (const [key, members] of groups) {
      if (members.length < 2) continue
      const heights = members.map((row) => rect(row).height)
      const low = Math.min(...heights)
      const high = Math.max(...heights)
      if (high - low <= TOLERANCE) continue
      const tallest = members[heights.indexOf(high)]
      out.push({
        check: 'row-heights',
        element: `${describe(container).replace(/ ".*?"/, '')} rows ${key}`,
        detail: `${members.length} rows range ${Math.round(low)}–${Math.round(high)}px; tallest: "${text(tallest, 60)}"`,
      })
    }
  }

  for (const table of Array.from(document.querySelectorAll('table'))) {
    if (!visible(table)) continue
    const rows = Array.from(table.querySelectorAll(':scope > tbody > tr'))
    if (rows.some((row) => getComputedStyle(row).display !== 'table-row')) continue
    unevenRows(table, rows, (row) => {
      const cells = Array.from(row.children)
      const spans = cells.some((cell) => cell instanceof HTMLTableCellElement && cell.colSpan > 1)
      return `[${row.className || 'tr'}${spans ? ', spanning' : ''}, ${cells.length} cells]`
    })
  }

  for (const list of Array.from(document.querySelectorAll('ul.list'))) {
    if (!visible(list)) continue
    const rows = Array.from(list.querySelectorAll(':scope > li.list-row:not(.list-row--wrap)'))
    unevenRows(list, rows, (row) => `[${row.className}]`)
  }

  // 4. The controls in one toolbar, card actions or page header are one height
  // and one style family. A control is the outermost button, field, select,
  // search box or chip (a chip that is a radio too); tabs, checkboxes,
  // switches, other radios and anything in a note are not. A page header's
  // own controls and its actions are one row.
  const CONTROL =
    'button, a.btn, input:not([type="checkbox"]):not([type="radio"]):not([type="hidden"]), select, textarea, [role="combobox"], .search, .chip'
  const NOT_A_CONTROL =
    '[role="tab"], [role="checkbox"], [role="switch"], [role="radio"]:not(.chip), .toolbar__note *'
  const ROWS = '.toolbar, .page-header, .page-header__actions, .card__actions'
  const HEADER_ACTIONS = '.page-header > .page-header__actions'
  const rowOf = (el: Element): Element | null => {
    const row = el.closest(ROWS)
    return row?.matches(HEADER_ACTIONS) ? row.parentElement : row
  }

  const opaque = (color: string): boolean => {
    const alpha = color.match(/rgba?\([^)]*?,\s*([\d.]+)\)$/)
    return color !== 'transparent' && (!alpha || parseFloat(alpha[1]) > 0)
  }
  /** Drawn as a box: a fill or a visible border, as a field, a chip or any
      button but a ghost one is. */
  const boxed = (el: Element): boolean => {
    const style = getComputedStyle(el)
    const border = parseFloat(style.borderTopWidth) > 0 && opaque(style.borderTopColor)
    return border || opaque(style.backgroundColor)
  }
  const textButton = (el: Element): boolean => el.matches('.btn:not(.btn--icon)')

  for (const row of Array.from(document.querySelectorAll(ROWS))) {
    if (!visible(row) || row.matches(HEADER_ACTIONS)) continue
    const controls = Array.from(row.querySelectorAll(CONTROL)).filter((el) => {
      if (!visible(el) || el.matches(NOT_A_CONTROL)) return false
      if (rowOf(el) !== row) return false
      const outer = el.parentElement?.closest(CONTROL)
      return !outer || !row.contains(outer)
    })
    if (controls.length < 2) continue
    const where = `${classes(row)}${place(row)}`
    const listed = (els: Element[], value: (el: Element) => string) =>
      els.map((el) => `${describe(el).replace(place(el), '')} ${value(el)}`).join(', ')

    const heights = controls.map((el) => Math.round(rect(el).height * 10) / 10)
    if (Math.max(...heights) - Math.min(...heights) > TOLERANCE) {
      out.push({
        check: 'control-heights',
        element: where,
        detail: listed(controls, (el) => `${heights[controls.indexOf(el)]}px`),
      })
    }

    const bare = controls.filter((el) => textButton(el) && !boxed(el))
    if (bare.length > 0 && bare.length < controls.length) {
      out.push({
        check: 'control-styles',
        element: where,
        detail: `a borderless text button beside boxed controls: ${listed(controls, (el) => (boxed(el) ? 'boxed' : 'bare'))}`,
      })
    }

    const worded = controls.filter(textButton)
    const padding = (el: Element) => getComputedStyle(el).paddingLeft
    if (new Set(worded.map(padding)).size > 1) {
      out.push({
        check: 'control-styles',
        element: where,
        detail: `text buttons with different padding: ${listed(worded, padding)}`,
      })
    }
  }

  // 5. A page header's actions end at the header's right edge.
  for (const header of Array.from(document.querySelectorAll('.page-header'))) {
    const actions = header.querySelector(':scope > .page-header__actions')
    if (!actions || !visible(header) || !visible(actions)) continue
    const style = getComputedStyle(header)
    const inner = rect(header).right - parseFloat(style.paddingRight) - parseFloat(style.borderRightWidth)
    const last = Array.from(actions.children).filter(visible)
    const end = Math.max(...last.map((el) => rect(el).right))
    if (Math.abs(inner - end) > 2) {
      out.push({
        check: 'header-actions',
        element: `.page-header${place(header)} actions`,
        detail: `actions end at ${Math.round(end)}px, the header's content edge is ${Math.round(inner)}px`,
      })
    }
  }

  // 6. The top bar shows the page's whole name.
  for (const title of Array.from(document.querySelectorAll('.header__title'))) {
    if (!visible(title) || title.scrollWidth <= title.clientWidth + TOLERANCE) continue
    out.push({
      check: 'header-title',
      element: describe(title),
      detail: `cut to ${title.clientWidth}px of ${title.scrollWidth}px`,
    })
  }

  // 7. Nothing in a bar is drawn over anything else in it. A bar is the top
  // bar, a page header, a toolbar, a card's header or an account's header; what
  // is compared is each control (a tab counts) and each run of text outside a
  // control, as painted: a line of text is measured by its glyphs, not its box,
  // and cut to whatever scroll or clip box it sits in.
  const BAR_GAP = 4
  const BARS = '.header, .page-header, .toolbar, .card__header, .context-card'
  const ATOM = `${CONTROL}, [role="tab"]`

  interface Box {
    left: number
    top: number
    right: number
    bottom: number
  }
  interface Atom {
    label: string
    /** A control, not a run of text, whose box is its painted edge. */
    control: boolean
    boxes: Box[]
  }

  const transparent = (el: Element, root: Element): boolean => {
    for (let at: Element | null = el; at && at !== root.parentElement; at = at.parentElement) {
      if (getComputedStyle(at).opacity === '0') return true
    }
    return false
  }

  const clipped = (box: Box, from: Element, root: Element): Box | null => {
    let cut: Box = { left: box.left, top: box.top, right: box.right, bottom: box.bottom }
    for (let at: Element | null = from; at && at !== root.parentElement; at = at.parentElement) {
      const style = getComputedStyle(at)
      const edge = rect(at)
      if (style.overflowX !== 'visible') {
        cut = { ...cut, left: Math.max(cut.left, edge.left), right: Math.min(cut.right, edge.right) }
      }
      if (style.overflowY !== 'visible') {
        cut = { ...cut, top: Math.max(cut.top, edge.top), bottom: Math.min(cut.bottom, edge.bottom) }
      }
    }
    return cut.right - cut.left > TOLERANCE && cut.bottom - cut.top > TOLERANCE ? cut : null
  }

  const barAtoms = (root: Element): Atom[] => {
    const atoms: Atom[] = []
    for (const el of Array.from(root.querySelectorAll(ATOM))) {
      if (!visible(el) || screenReaderOnly(el) || transparent(el, root)) continue
      const outer = el.parentElement?.closest(ATOM)
      if (outer && root.contains(outer)) continue
      const box = clipped(rect(el), el.parentElement ?? el, root)
      if (box) atoms.push({ label: describe(el).replace(place(el), ''), control: true, boxes: [box] })
    }
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const parent = node.parentElement
      const words = (node.textContent ?? '').trim()
      if (!parent || words === '' || parent.closest(ATOM)) continue
      if (!visible(parent) || transparent(parent, root)) continue
      if (parent.closest('.visually-hidden, .sr-only')) continue
      const range = document.createRange()
      range.selectNodeContents(node)
      const boxes = Array.from(range.getClientRects())
        .map((line) => clipped(line, parent, root))
        .filter((box): box is Box => box !== null)
      if (boxes.length > 0) {
        const short = words.length > 30 ? `${words.slice(0, 29)}…` : words
        atoms.push({ label: `text "${short}"`, control: false, boxes })
      }
    }
    return atoms
  }

  const overlap = (a: Box, b: Box): number => {
    const wide = Math.min(a.right, b.right) - Math.max(a.left, b.left)
    const tall = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top)
    return wide > TOLERANCE && tall > TOLERANCE ? Math.round(Math.min(wide, tall)) : 0
  }

  /**
   * `gap` grows a control's boxes when both atoms are controls, so two that
   * touch count as drawn over each other. Text is measured by its glyphs,
   * which sit tighter than any gap.
   */
  const clashes = (first: Atom[], second: Atom[] | null, gap = 0): string[] => {
    const found: string[] = []
    const grow = (box: Box, by: number): Box => ({
      left: box.left - by,
      top: box.top - by,
      right: box.right + by,
      bottom: box.bottom + by,
    })
    for (let i = 0; i < first.length && found.length < 3; i += 1) {
      const others = second ?? first.slice(i + 1)
      for (let j = 0; j < others.length && found.length < 3; j += 1) {
        const by = first[i].control && others[j].control ? gap : 0
        const depth = Math.max(
          ...first[i].boxes.flatMap((a) => others[j].boxes.map((b) => overlap(grow(a, by), b))),
        )
        if (depth > 0) found.push(`${first[i].label} and ${others[j].label} by ${depth}px`)
      }
    }
    return found
  }

  const bars: { name: string; card: Element | null; atoms: Atom[] }[] = []
  for (const root of Array.from(document.querySelectorAll(BARS))) {
    if (!visible(root)) continue
    const outer = root.parentElement?.closest(BARS)
    if (outer && visible(outer)) continue
    bars.push({ name: `${classes(root)}${place(root)}`, card: root.closest('.card'), atoms: barAtoms(root) })
  }

  // Within a bar, and between two bars of one card: a card header's actions
  // that wrap past the header land on the toolbar under it, and two bars
  // that merely touch read as one control drawn over the next on a phone.
  // Bars of different cards are not compared, since the sticky top bar
  // passes over every one of them on a scroll.
  for (const [i, bar] of bars.entries()) {
    const found = clashes(bar.atoms, null)
    if (found.length > 0) out.push({ check: 'overlap', element: bar.name, detail: found.join('; ') })
    for (const other of bars.slice(i + 1)) {
      if (bar.card === null || other.card !== bar.card) continue
      const across = clashes(bar.atoms, other.atoms, BAR_GAP)
      if (across.length > 0) {
        out.push({ check: 'overlap', element: `${bar.name} and ${other.name}`, detail: across.join('; ') })
      }
    }
  }

  // 8. Every band of a card starts its content on the card's `--card-inset`:
  // the header, its toolbars, the rows of a list, a table or the register,
  // and the register's group headings. What is compared is where a band's
  // padding puts its content, or for a row of cells, where its first cell's
  // does; an indent inside that edge (a tree's depth) is the content's own.
  const ALIGNED_BANDS = [
    '.card__header',
    '.toolbar',
    '.calendar',
    '.register__selectall',
    '.register__group',
    '.txn-line',
    ':is(.card--flush, .card__body) > .list > .list-row',
    ':is(.card--flush, .card__body) > .table-frame > .table > * > tr',
  ].join(', ')
  const CELL_ROWS = '.register__head, .register__row:not(.register__row--stacked)'
  const rootPx = parseFloat(getComputedStyle(document.documentElement).fontSize)
  const length = (value: string): number =>
    value.trim().endsWith('rem') ? parseFloat(value) * rootPx : parseFloat(value)
  const contentEdge = (el: Element): number => {
    const style = getComputedStyle(el)
    return rect(el).left + parseFloat(style.borderLeftWidth) + parseFloat(style.paddingLeft)
  }

  for (const card of Array.from(document.querySelectorAll('.card'))) {
    if (!visible(card)) continue
    const style = getComputedStyle(card)
    const expected = rect(card).left + parseFloat(style.borderLeftWidth) + length(style.getPropertyValue('--card-inset'))
    const misplaced = new Map<string, { at: number; count: number }>()
    const bands = Array.from(card.querySelectorAll(`${ALIGNED_BANDS}, ${CELL_ROWS}`)).filter(
      (band) => band.closest('.card') === card && visible(band),
    )
    for (const band of bands) {
      const lead = band.matches(CELL_ROWS) || (band.matches('tr') && getComputedStyle(band).display === 'table-row')
        ? Array.from(band.children).find(visible)
        : band
      if (!lead) continue
      const at = contentEdge(lead) - expected
      if (Math.abs(at) <= TOLERANCE) continue
      const key = `${band.tagName.toLowerCase()}${classes(band)}`
      const seen = misplaced.get(key)
      misplaced.set(key, { at: seen?.at ?? at, count: (seen?.count ?? 0) + 1 })
    }
    for (const [key, { at, count }] of misplaced) {
      out.push({
        check: 'alignment',
        element: `${key}${place(card.firstElementChild ?? card)}`,
        detail: `${count > 1 ? `${count} of them start` : 'starts'} ${Math.round(at * 10) / 10}px off the card's inset`,
      })
    }
  }

  return out
}
