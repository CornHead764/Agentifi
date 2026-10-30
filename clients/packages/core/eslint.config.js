import js from '@eslint/js'
import { defineConfig, globalIgnores } from 'eslint/config'
import globals from 'globals'
import tseslint from 'typescript-eslint'

// The browser's own globals: those neither the language nor Node also has.
// React Native has none of them.
const DOM_GLOBALS = Object.keys(globals.browser).filter(
  (name) => !(name in globals.es2025) && !(name in globals['shared-node-browser']),
)

const UI_PACKAGES = ['react', 'react-dom', 'react-native']

export default defineConfig([
  globalIgnores(['test/fixtures']),
  {
    files: ['**/*.ts'],
    extends: [js.configs.recommended, tseslint.configs.recommended],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.es2025,
    },
    rules: {
      'no-restricted-globals': [
        'error',
        ...DOM_GLOBALS.map((name) => ({
          name,
          message: '@agentifi/core runs on the web and in React Native, so it uses no DOM global.',
        })),
      ],
      'no-restricted-imports': [
        'error',
        {
          paths: UI_PACKAGES.map((name) => ({
            name,
            message: '@agentifi/core holds no UI code; it imports neither React nor a renderer.',
          })),
          patterns: [
            {
              group: UI_PACKAGES.map((name) => `${name}/*`),
              message: '@agentifi/core holds no UI code; it imports neither React nor a renderer.',
            },
          ],
        },
      ],
      // Only `money.ts` makes a `Money`. A cast anywhere else gives a raw wire
      // string a `Money` type without ever parsing it.
      '@typescript-eslint/consistent-type-assertions': ['error', { assertionStyle: 'never' }],
      'no-restricted-syntax': [
        'error',
        {
          selector:
            "CallExpression[callee.property.name='toFixed'][callee.object.type='BinaryExpression'][callee.object.operator='/'][callee.object.right.value=100]",
          message: 'Money to text goes through amountToInput or amountToWire in @agentifi/core/money.',
        },
        {
          selector:
            "CallExpression[callee.property.name='toFixed'][callee.object.callee.name='moneyToNumber']",
          message: 'Money to text goes through amountToInput or amountToWire in @agentifi/core/money.',
        },
      ],
    },
  },
  {
    files: ['src/money.ts', '**/*.test.ts'],
    rules: {
      '@typescript-eslint/consistent-type-assertions': 'off',
    },
  },
])
