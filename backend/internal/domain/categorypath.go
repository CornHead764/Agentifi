package domain

import "slices"

// CategoryPath walks id's ancestors through lookup and returns their names,
// root first and id's own name last. lookup returning ok false, for the
// sentinel parent or an id the caller's set does not hold, ends the walk; so
// does a second visit to an id already on the path, which stops a parent
// cycle in the data from looping it forever. The caller folds and joins the
// names its own way: a path that matches another by name takes one fold, a
// path that is only shown to a person takes none.
func CategoryPath[K comparable](id K, lookup func(K) (name string, parent K, ok bool)) []string {
	seen := map[K]bool{}
	var names []string
	for at := id; !seen[at]; {
		name, parent, ok := lookup(at)
		if !ok {
			break
		}
		seen[at] = true
		names = append(names, name)
		at = parent
	}
	slices.Reverse(names)
	return names
}
