/**
 * The design tokens, and the CSS the web app reads them as. `tokens.json` is
 * the source; `renderCss` writes it out as the web app's `styles/tokens.css`.
 */

import file from '../tokens.json' with { type: 'json' }

export interface Token {
  /** The custom property's name without its leading `--`. */
  token: string
  value: string
}

/** A comment's lines, without the comment markers or the indent. */
export interface Comment {
  comment: string[]
  /** Spaces before the comment, when it is not the rule's own token indent. */
  indent?: number
}

export interface Rule {
  comment?: string[]
  media?: string
  selector: string
  /** Runs of tokens and comments; a blank line separates two groups. */
  groups: (Token | Comment)[][]
}

export interface TokenFile {
  /** The file's opening comments, verbatim. */
  header: string[]
  rules: Rule[]
}

export const tokens: TokenFile = file

export function renderCss(source: TokenFile = tokens): string {
  const blocks = [source.header, ...source.rules.map(renderRule)]
  return `${blocks.map((lines) => lines.join('\n')).join('\n\n')}\n`
}

function renderRule(rule: Rule): string[] {
  const outer = rule.media === undefined ? 0 : 2
  const inner = outer + 2
  const groups = rule.groups.map((group) =>
    group.flatMap((item) =>
      'token' in item
        ? [`${' '.repeat(inner)}--${item.token}: ${item.value};`]
        : renderComment(item.comment, item.indent ?? inner),
    ),
  )
  const block = [
    `${' '.repeat(outer)}${rule.selector} {`,
    ...groups.flatMap((group, i) => (i === 0 ? group : ['', ...group])),
    `${' '.repeat(outer)}}`,
  ]
  return [
    ...(rule.comment === undefined ? [] : renderComment(rule.comment, 0)),
    ...(rule.media === undefined ? block : [`@media ${rule.media} {`, ...block, '}']),
  ]
}

/** A block comment whose later lines sit three spaces past its opening. */
function renderComment(lines: string[], indent: number): string[] {
  const pad = ' '.repeat(indent)
  return lines.map(
    (line, i) => `${pad}${i === 0 ? '/* ' : '   '}${line}${i === lines.length - 1 ? ' */' : ''}`,
  )
}
