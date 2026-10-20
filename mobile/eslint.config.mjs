import js from '../frontend/node_modules/@eslint/js/src/index.js'
import globals from '../frontend/node_modules/globals/index.js'

export default [
  { ignores: ['dist/**', 'ios/**', 'test/output/**'] },
  js.configs.recommended,
  { languageOptions: { globals: { ...globals.browser, ...globals.node } } },
]
