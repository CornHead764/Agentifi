/** One line cut with an ellipsis; the whole text is the tooltip. */
export function Clipped({ text, sub = false }: { text: string; sub?: boolean }) {
  return (
    <span className={sub ? 'cell__sub cell__clip' : 'cell__clip'} title={text}>
      {text}
    </span>
  )
}

/**
 * A list in a two-line row: the first entry, then the rest on one clipped
 * line. The tooltip on either line names every entry.
 */
export function TwoLines({ items }: { items: string[] }) {
  const [first, ...rest] = items
  const all = items.join('\n')
  return (
    <>
      <span className="cell__clip" title={all}>
        {first}
      </span>
      {rest.length > 0 ? (
        <span className="cell__sub cell__clip" title={all}>
          {rest.join(', ')}
        </span>
      ) : null}
    </>
  )
}
