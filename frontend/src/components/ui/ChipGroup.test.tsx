import type { KeyboardEvent, ReactElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { nextChipIndex } from './chip-keys'
import { ChipGroup } from './ChipGroup'

const OPTIONS = [
  { value: 'twr', label: 'TWR' },
  { value: 'irr', label: 'IRR' },
  { value: 'mwr', label: 'MWR' },
] as const

type Metric = (typeof OPTIONS)[number]['value']

describe('nextChipIndex', () => {
  it('moves one either way and wraps at the ends', () => {
    expect(nextChipIndex('ArrowRight', 0, 3)).toBe(1)
    expect(nextChipIndex('ArrowDown', 2, 3)).toBe(0)
    expect(nextChipIndex('ArrowLeft', 0, 3)).toBe(2)
    expect(nextChipIndex('ArrowUp', 2, 3)).toBe(1)
  })

  it('jumps to the ends, and ignores every other key', () => {
    expect(nextChipIndex('Home', 2, 3)).toBe(0)
    expect(nextChipIndex('End', 0, 3)).toBe(2)
    expect(nextChipIndex('Enter', 1, 3)).toBeNull()
    expect(nextChipIndex('ArrowRight', 0, 0)).toBeNull()
  })
})

describe('<ChipGroup>', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('is a radio group with only the chosen chip in the tab order', () => {
    const markup = renderToStaticMarkup(
      <ChipGroup label="Return measure" value="irr" options={OPTIONS} onChange={() => undefined} />,
    )
    expect(markup).toContain('role="radiogroup" aria-label="Return measure"')
    const radios = [...markup.matchAll(/<button[^>]*>/g)].map((match) => match[0])
    expect(radios.map((radio) => /aria-checked="true"/.test(radio))).toEqual([false, true, false])
    expect(radios.map((radio) => /tabindex="0"/.test(radio))).toEqual([false, true, false])
    expect(radios[1]).toContain('class="chip chip--on"')
  })

  it('moves the choice, and the focus, with the arrow keys', () => {
    const chosen: Metric[] = []
    const focused: number[] = []
    const radios = OPTIONS.map((_, index) => ({ focus: () => focused.push(index) }))
    vi.stubGlobal('document', { activeElement: radios[2] })

    const group = ChipGroup<Metric>({
      label: 'Return measure',
      value: 'mwr',
      options: OPTIONS,
      onChange: (value) => chosen.push(value),
    }) as ReactElement<{ onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => void }>
    const press = (key: string) =>
      group.props.onKeyDown({
        key,
        currentTarget: { querySelectorAll: () => radios },
        preventDefault: () => undefined,
      } as unknown as KeyboardEvent<HTMLDivElement>)

    press('ArrowRight')
    press('ArrowLeft')
    press('Tab')
    expect(chosen).toEqual(['twr', 'irr'])
    expect(focused).toEqual([0, 1])
  })
})
