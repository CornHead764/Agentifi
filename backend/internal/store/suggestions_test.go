package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A dismissed suggestion has to stay dismissed: the Suggested tab re-derives
// its proposals on every read, so this table is the only thing that remembers.

const signature = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestADismissalSurvivesAndIsIdempotent(t *testing.T) {
	ctx := t.Context()
	space := newSpace(t)

	require.NoError(t, db(t).DismissSuggestion(ctx, space, signature))
	require.NoError(t, db(t).DismissSuggestion(ctx, space, signature))

	dismissed, err := db(t).ListDismissedSuggestions(ctx, space)
	require.NoError(t, err)
	require.Equal(t, []string{signature}, dismissed)
}

func TestRestoringPutsASuggestionBackInPlay(t *testing.T) {
	ctx := t.Context()
	space := newSpace(t)
	require.NoError(t, db(t).DismissSuggestion(ctx, space, signature))

	restored, err := db(t).RestoreSuggestion(ctx, space, signature)
	require.NoError(t, err)
	require.True(t, restored)

	dismissed, err := db(t).ListDismissedSuggestions(ctx, space)
	require.NoError(t, err)
	require.Empty(t, dismissed)
}

func TestRestoringSomethingNeverDismissedSaysSo(t *testing.T) {
	// So the API can answer a 404.
	restored, err := db(t).RestoreSuggestion(t.Context(), newSpace(t), signature)
	require.NoError(t, err)
	require.False(t, restored)
}

func TestADismissalBelongsToOneSpace(t *testing.T) {
	ctx := t.Context()
	mine, theirs := newSpace(t), newSpace(t)
	require.NoError(t, db(t).DismissSuggestion(ctx, mine, signature))

	dismissed, err := db(t).ListDismissedSuggestions(ctx, theirs)
	require.NoError(t, err)
	require.Empty(t, dismissed, "one household's dismissal hid another's suggestion")

	// The same signature can be dismissed independently in both.
	require.NoError(t, db(t).DismissSuggestion(ctx, theirs, signature))
	both, err := db(t).ListDismissedSuggestions(ctx, theirs)
	require.NoError(t, err)
	require.Len(t, both, 1)
}
