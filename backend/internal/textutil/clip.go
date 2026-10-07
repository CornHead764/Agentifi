// Package textutil holds text and small generic helpers with no knowledge
// of any domain.
package textutil

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Capitalize upper-cases the first letter and leaves the rest as written.
func Capitalize(value string) string {
	first, size := utf8.DecodeRuneInString(value)
	if size == 0 {
		return value
	}
	return string(unicode.ToUpper(first)) + value[size:]
}

// FirstNonBlank is the first value with anything but whitespace in it,
// trimmed, or "" when every value is blank.
func FirstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// FirstLine is the text up to its first line break, CRLF or LF alike,
// trimmed; the whole text, trimmed, when it carries none.
func FirstLine(text string) string {
	return strings.TrimSpace(strings.SplitN(strings.ReplaceAll(text, "\r\n", "\n"), "\n", 2)[0])
}

// Clip caps value at a number of characters rather than bytes, so it never
// splits a UTF-8 sequence.
func Clip(value string, runes int) string {
	count := 0
	for at := range value {
		if count == runes {
			return value[:at]
		}
		count++
	}
	return value
}

// ClipMarked is Clip with an ellipsis where anything was cut off, the cut
// edge trimmed first so the ellipsis never follows a severed space.
func ClipMarked(value string, runes int) string {
	clipped := Clip(value, runes)
	if len(clipped) < len(value) {
		return strings.TrimSpace(clipped) + "…"
	}
	return clipped
}
