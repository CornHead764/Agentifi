/** Writes the tokens as CSS: `node generate.ts <file.css>`. */

import { writeFileSync } from 'node:fs'

import { renderCss } from './index.ts'

const [out] = process.argv.slice(2)
if (out === undefined) {
  console.error('usage: node generate.ts <file.css>')
  process.exit(2)
}
writeFileSync(out, renderCss())
