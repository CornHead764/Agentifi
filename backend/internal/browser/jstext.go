package browser

// CleanJS declares clean(value): a string's runs of whitespace collapsed to
// one space each and trimmed, the form a reading is compared and capped in.
// browser/agent re-exports this as agent.CleanJS, since browser/agent
// imports browser and this package cannot import it back.
const CleanJS = `
const clean = (value) => String(value || '').replace(/\s+/g, ' ').trim();
`

// EscapedJS declares escaped(value): value through CSS.escape, the form an
// id or attribute value must take to drop safely into a selector, or value
// itself where this page has no CSS.escape to call. browser/agent re-exports
// this as agent.EscapedJS.
const EscapedJS = `
const escaped = (value) => (window.CSS && CSS.escape ? CSS.escape(value) : value);
`
