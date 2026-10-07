/**
 * The subset of Markdown a model writes, as React nodes; anything unrecognised
 * prints as plain text. Nodes, never an HTML string: the content quotes a
 * household's payees, and React escaping is the injection defence.
 */

import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'

interface Block {
  kind: 'code' | 'text'
  language: string
  lines: string[]
}

/** Splits on ``` fences, which are the one thing that suspends every other rule. */
export function blocksOf(source: string): Block[] {
  const out: Block[] = []
  let current: Block = { kind: 'text', language: '', lines: [] }
  for (const line of source.split('\n')) {
    const fence = /^\s*```(.*)$/.exec(line)
    if (fence) {
      if (current.lines.length > 0 || current.kind === 'code') out.push(current)
      current =
        current.kind === 'code'
          ? { kind: 'text', language: '', lines: [] }
          : { kind: 'code', language: fence[1].trim(), lines: [] }
      continue
    }
    current.lines.push(line)
  }
  if (current.lines.length > 0) out.push(current)
  return out
}

/**
 * One line's inline marks. `[text](/path)` links only to a path inside this
 * app: a model quoting somebody's mail must not put a stranger's URL on screen.
 */
export function inlineNodes(text: string, keyPrefix = ''): ReactNode[] {
  const out: ReactNode[] = []
  // Code first: a backtick span is literal, so marks inside it are not marks.
  const pattern = /(`[^`]+`)|(\[[^\]]+\]\(\/(?!\/)[^)\s]*\))|(\*\*[^*]+\*\*)|(\*[^*]+\*)/g
  let last = 0
  let match: RegExpExecArray | null
  let index = 0
  while ((match = pattern.exec(text)) !== null) {
    if (match.index > last) out.push(text.slice(last, match.index))
    const token = match[0]
    const key = `${keyPrefix}i${index++}`
    if (token.startsWith('`')) {
      out.push(<code key={key}>{token.slice(1, -1)}</code>)
    } else if (token.startsWith('[')) {
      const split = token.indexOf('](')
      out.push(
        <Link key={key} to={token.slice(split + 2, -1)}>
          {token.slice(1, split)}
        </Link>,
      )
    } else if (token.startsWith('**')) {
      out.push(<strong key={key}>{token.slice(2, -2)}</strong>)
    } else {
      out.push(<em key={key}>{token.slice(1, -1)}</em>)
    }
    last = match.index + token.length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

const BULLET = /^\s*[-*+]\s+(.*)$/
const NUMBERED = /^\s*(\d+)[.)]\s+(.*)$/
const HEADING = /^\s*(#{1,4})\s+(.*)$/

function textNodes(lines: string[], keyPrefix: string): ReactNode[] {
  const out: ReactNode[] = []
  let paragraph: string[] = []
  let list: { ordered: boolean; items: string[] } | null = null
  let index = 0
  const key = () => `${keyPrefix}b${index++}`

  const flushParagraph = () => {
    if (paragraph.length === 0) return
    const k = key()
    out.push(<p key={k}>{inlineNodes(paragraph.join(' '), k)}</p>)
    paragraph = []
  }
  const flushList = () => {
    if (list === null) return
    const k = key()
    const items = list.items.map((item, i) => <li key={`${k}-${i}`}>{inlineNodes(item, `${k}-${i}`)}</li>)
    out.push(list.ordered ? <ol key={k}>{items}</ol> : <ul key={k}>{items}</ul>)
    list = null
  }

  for (const line of lines) {
    if (line.trim() === '') {
      flushParagraph()
      flushList()
      continue
    }
    const heading = HEADING.exec(line)
    if (heading) {
      flushParagraph()
      flushList()
      const k = key()
      // Below the page's own headings: a reply must not outrank the screen.
      const body = inlineNodes(heading[2], k)
      out.push(heading[1].length <= 2 ? <h4 key={k}>{body}</h4> : <h5 key={k}>{body}</h5>)
      continue
    }
    const numbered = NUMBERED.exec(line)
    const bullet = BULLET.exec(line)
    if (numbered || bullet) {
      flushParagraph()
      const ordered = Boolean(numbered)
      const item = numbered ? numbered[2] : bullet![1]
      if (list === null || list.ordered !== ordered) {
        flushList()
        list = { ordered, items: [] }
      }
      list.items.push(item)
      continue
    }
    flushList()
    paragraph.push(line.trim())
  }
  flushParagraph()
  flushList()
  return out
}

export function renderMarkdown(source: string): ReactNode[] {
  const out: ReactNode[] = []
  blocksOf(source).forEach((block, i) => {
    if (block.kind === 'code') {
      out.push(
        <pre key={`c${i}`} className="chat__code">
          <code>{block.lines.join('\n')}</code>
        </pre>,
      )
      return
    }
    out.push(...textNodes(block.lines, `t${i}-`))
  })
  return out
}
