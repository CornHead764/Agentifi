package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTagsAreListedIgnoringCase(t *testing.T) {
	space := newSpace(t)
	for _, name := range []string{"cherry", "apple", "Banana"} {
		newTag(t, space, name)
	}
	tags, err := db(t).ListTags(t.Context(), space, false)
	require.NoError(t, err)
	var names []string
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	require.Equal(t, []string{"apple", "Banana", "cherry"}, names)
}
