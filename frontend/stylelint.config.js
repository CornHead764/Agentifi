/*
 * Only the rules that keep the stylesheets on the design tokens; layout and
 * formatting are left to review. What each token is for is in
 * src/styles/tokens.css and docs/development.md.
 */

/* A length other than 0 and a hairline: every spacing, size and radius is a token. */
const RAW_LENGTH = /(?<![\w.-])(?!0(?:px|rem)\b)(?!1px\b)\d*\.?\d+(?:px|rem)\b/

/* The breakpoint ladder, in rem because a media query's rem is always 16px. */
const LADDER = ['30rem', '40rem', '48rem', '56rem', '64rem', '70rem', '86rem']

export default {
  rules: {
    'color-no-hex': true,
    'function-disallowed-list': ['rgb', 'rgba', 'hsl', 'hsla'],
    // A mask reads only the alpha channel, where `black` means opaque.
    'color-named': ['never', { ignoreProperties: ['/^mask/', '/^-webkit-mask/'] }],
    'declaration-property-value-disallowed-list': [
      {
        '/^(padding|margin|gap|row-gap|column-gap|font-size|border-radius)/': [RAW_LENGTH],
        'font-weight': ['/^\\d+$/'],
      },
      { message: (property) => `${property} takes a token from tokens.css, not a raw value` },
    ],
    'media-feature-name-value-allowed-list': {
      'max-width': LADDER,
      'min-width': LADDER,
    },
  },
  overrides: [
    {
      files: ['src/styles/tokens.css'],
      rules: {
        'color-no-hex': null,
        'function-disallowed-list': null,
        'color-named': null,
      },
    },
  ],
}
