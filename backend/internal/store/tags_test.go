package store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTagRoundTrip(t *testing.T) {
	space := newSpace(t)
	st, ctx := db(t), t.Context()
	color := func(id uuid.UUID) any {
		t.Helper()
		var stored any
		require.NoError(t, householdDB(t, space).Read().QueryRowContext(ctx, `SELECT color FROM tags WHERE id = ?`, id).Scan(&stored))
		return stored
	}

	trip := &Tag{Name: "Trip"}
	require.NoError(t, st.CreateTag(ctx, space, trip))
	require.NotEqual(t, uuid.Nil, trip.ID)
	require.Equal(t, space, trip.SpaceID)
	require.False(t, trip.CreatedAt.IsZero())
	require.Equal(t, trip.CreatedAt, trip.UpdatedAt)
	require.Nil(t, color(trip.ID))
	got, err := st.GetTag(ctx, space, trip.ID)
	require.NoError(t, err)
	require.Equal(t, *trip, got)

	work := &Tag{ID: uuid.New(), Name: "Work", Color: "#00aa00"}
	require.NoError(t, st.CreateTag(ctx, space, work))
	require.Equal(t, "#00aa00", color(work.ID))

	trip.Name, trip.Color = "Holiday", "#aa0000"
	require.NoError(t, st.UpdateTag(ctx, space, trip))
	got, err = st.GetTag(ctx, space, trip.ID)
	require.NoError(t, err)
	require.Equal(t, *trip, got)
	trip.Color = ""
	require.NoError(t, st.UpdateTag(ctx, space, trip))
	require.Nil(t, color(trip.ID))

	require.NoError(t, st.DeleteTag(ctx, space, work.ID))
	live, err := st.ListTags(ctx, space, false)
	require.NoError(t, err)
	require.Equal(t, []Tag{*trip}, live)
	all, err := st.ListTags(ctx, space, true)
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, []string{"Holiday", "Work"}, []string{all[0].Name, all[1].Name})
	require.True(t, all[1].IsDeleted)
	require.Equal(t, space, all[1].SpaceID)

	missing := uuid.New()
	_, err = st.GetTag(ctx, space, missing)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, st.UpdateTag(ctx, space, &Tag{ID: missing, Name: "Nobody"}), ErrNotFound)
	require.ErrorIs(t, st.DeleteTag(ctx, space, missing), ErrNotFound)
}

// An empty list is [] on the wire, not null.
func TestEmptyTagListIsNotNil(t *testing.T) {
	tags, err := db(t).ListTags(t.Context(), newSpace(t), true)
	require.NoError(t, err)
	require.NotNil(t, tags)
	require.Empty(t, tags)
}
