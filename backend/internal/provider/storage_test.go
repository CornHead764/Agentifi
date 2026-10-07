package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func newLocalStorage(t *testing.T) *LocalStorage {
	t.Helper()
	return &LocalStorage{BasePath: t.TempDir()}
}

func TestUploadWritesTheFileAndReportsItsSize(t *testing.T) {
	store := newLocalStorage(t)
	stored, err := store.Upload(context.Background(), "receipts/a.pdf", []byte("hello"), "application/pdf")
	require.NoError(t, err)
	require.Equal(t, StoredFile{StorageKey: "receipts/a.pdf", Size: 5, ContentType: "application/pdf"}, stored)

	onDisk, err := os.ReadFile(filepath.Join(store.BasePath, "receipts", "a.pdf"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(onDisk))
}

func TestUploadCreatesTheParentDirectories(t *testing.T) {
	store := newLocalStorage(t)
	_, err := store.Upload(context.Background(), "a/b/c/d.txt", []byte("x"), "text/plain")
	require.NoError(t, err)

	data, err := store.Download(context.Background(), "a/b/c/d.txt")
	require.NoError(t, err)
	require.Equal(t, "x", string(data))
}

func TestDownloadingAMissingKeyIsNotFoundNotAStorageFailure(t *testing.T) {
	store := newLocalStorage(t)
	_, err := store.Download(context.Background(), "nope.pdf")
	require.ErrorIs(t, err, ErrFileNotFound)
}

func TestDeleteIsSafeToRetry(t *testing.T) {
	store := newLocalStorage(t)
	_, err := store.Upload(context.Background(), "a.txt", []byte("x"), "text/plain")
	require.NoError(t, err)

	require.NoError(t, store.Delete(context.Background(), "a.txt"))
	require.NoError(t, store.Delete(context.Background(), "a.txt"))
}

func TestATraversingKeyNeverEscapesTheBaseDirectory(t *testing.T) {
	// Storage keys are built from user-supplied filenames.
	store := newLocalStorage(t)
	for _, key := range []string{"../escape.txt", "a/../../escape.txt", "/etc/passwd", "", "."} {
		_, err := store.Upload(context.Background(), key, []byte("x"), "text/plain")
		require.Error(t, err, "upload accepted %q", key)

		_, err = store.Download(context.Background(), key)
		require.Error(t, err, "download accepted %q", key)
		require.False(t, errors.Is(err, ErrFileNotFound), "%q was reported as merely missing", key)

		require.Error(t, store.Delete(context.Background(), key), "delete accepted %q", key)
	}
}

func TestLocalFilesAreStreamedThroughTheAPIRatherThanLinkedTo(t *testing.T) {
	store := newLocalStorage(t)
	require.Equal(t, "local", store.Name())
	require.Empty(t, store.URL("a.txt"))
}
