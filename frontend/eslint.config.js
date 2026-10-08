import js from '@eslint/js'
import { defineConfig, globalIgnores } from 'eslint/config'
import jsxA11y from 'eslint-plugin-jsx-a11y'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import globals from 'globals'
import tseslint from 'typescript-eslint'

const STYLE_PROPERTY =
  "JSXAttribute[name.name='style'] > JSXExpressionContainer > ObjectExpression > Property"
const DATA_DRIVEN =
  '/^(color|background|backgroundColor|width|height|transform|left|right|gridTemplateColumns|fontSize)$/'

export default defineConfig([
  // src/gen is buf generate's output, checked by CI against the protos instead.
  globalIgnores(['dist', 'src/gen']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      'jsx-a11y': jsxA11y,
    },
    rules: {
      'react-refresh/only-export-components': 'warn',
      'jsx-a11y/anchor-has-content': ['error', { components: ['Link'] }],
      // Money coercion happens in `lib/api.ts` and `lib/rpc/wire.ts` and nowhere else. A cast in a
      // page is how a raw wire string gets a `Money` type without ever being
      // parsed, which is the exact failure the boundary exists to prevent.
      '@typescript-eslint/consistent-type-assertions': ['error', { assertionStyle: 'never' }],
      'no-restricted-syntax': [
        'error',
        {
          selector:
            "CallExpression[callee.property.name='toFixed'][callee.object.type='BinaryExpression'][callee.object.operator='/'][callee.object.right.value=100]",
          message: 'Money to text goes through amountToInput or amountToWire in lib/money.',
        },
        {
          selector:
            "CallExpression[callee.property.name='toFixed'][callee.object.callee.name='moneyToNumber']",
          message: 'Money to text goes through amountToInput or amountToWire in lib/money.',
        },
        // An inline style carries only what the data decides: a swatch's colour,
        // a bar's length, a column's width. Everything else is a class, so it
        // takes the tokens and the stylelint rules with it.
        {
          selector: `${STYLE_PROPERTY}[key.name!=${DATA_DRIVEN}][key.value!=/^--/]`,
          message: 'Style this with a class in src/styles; an inline style holds only data-driven values.',
        },
        {
          selector: `${STYLE_PROPERTY}[key.name=${DATA_DRIVEN}][value.type='Literal']`,
          message: 'A fixed value is not data-driven; give it a class in src/styles.',
        },
      ],
    },
  },
  {
    files: ['src/lib/money.ts', 'src/lib/api.ts', 'src/lib/rpc/wire.ts', '**/*.test.{ts,tsx}'],
    rules: {
      '@typescript-eslint/consistent-type-assertions': 'off',
    },
  },
])
