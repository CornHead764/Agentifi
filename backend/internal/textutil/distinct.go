package textutil

// Distinct keeps each non-zero value once, in the order it first appeared.
func Distinct[T comparable](values []T) []T {
	var zero T
	seen := make(map[T]bool, len(values))
	out := make([]T, 0, len(values))
	for _, value := range values {
		if value == zero || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
