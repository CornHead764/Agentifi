import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { useSettled } from './useSettled'

function Probe({ value }: { value: string }) {
  return <>{useSettled(value)}</>
}

describe('useSettled', () => {
  it('starts at the value it is first given', () => {
    expect(renderToStaticMarkup(<Probe value="1250" />)).toBe('1250')
  })
})
