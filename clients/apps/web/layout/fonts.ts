import { fileURLToPath } from 'node:url'

/**
 * The faces the stylesheets ask for first, served from `node_modules`, so a
 * row wraps at the same word on every machine. Without them the page falls
 * back to whatever sans-serif and monospace the host has installed, and a
 * width check that passes on one host fails on another. The workspace keeps
 * `node_modules` in `clients/`, above vite's root, so they are asked for by path.
 */
const FILES = `/@fs${fileURLToPath(new URL('../../../node_modules/@fontsource-variable', import.meta.url))}`

export const FONT_CSS = `
@font-face {
  font-family: 'Inter';
  font-style: normal;
  font-weight: 100 900;
  src: url('${FILES}/inter/files/inter-latin-wght-normal.woff2') format('woff2');
}
@font-face {
  font-family: 'Inter';
  font-style: italic;
  font-weight: 100 900;
  src: url('${FILES}/inter/files/inter-latin-wght-italic.woff2') format('woff2');
}
@font-face {
  font-family: 'SF Mono';
  font-style: normal;
  font-weight: 100 800;
  src: url('${FILES}/jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2') format('woff2');
}
`
