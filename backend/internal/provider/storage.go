package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type StoredFile struct {
	StorageKey  string
	Size        int64
	ContentType string
}

// ErrFileNotFound is a 404; any other Download error is a storage failure.
var ErrFileNotFound = errors.New("storage: file not found")

type StorageProvider interface {
	Name() string

	Upload(ctx context.Context, storageKey string, data []byte, contentType string) (StoredFile, error)
	Download(ctx context.Context, storageKey string) ([]byte, error)
	Delete(ctx context.Context, storageKey string) error

	// URL is empty when the file must be streamed through the API.
	URL(storageKey string) string
}

type LocalStorage struct {
	BasePath string
}

func (l *LocalStorage) Name() string { return "local" }

// resolve rejects any key escaping BasePath: keys are built from user-supplied
// filenames.
func (l *LocalStorage) resolve(storageKey string) (string, error) {
	base, err := filepath.Abs(l.BasePath)
	if err != nil {
		return "", fmt.Errorf("storage: base path: %w", err)
	}
	// Join would quietly contain an absolute key, hiding a caller bug that can
	// make two attachments share a path.
	if filepath.IsAbs(storageKey) {
		return "", fmt.Errorf("storage: storage key %q must be relative", storageKey)
	}
	full := filepath.Clean(filepath.Join(base, storageKey))
	if full != base && !strings.HasPrefix(full, base+string(filepath.Separator)) {
		return "", fmt.Errorf("storage: invalid storage key %q", storageKey)
	}
	if full == base {
		return "", fmt.Errorf("storage: invalid storage key %q", storageKey)
	}
	return full, nil
}

func (l *LocalStorage) Upload(_ context.Context, storageKey string, data []byte, contentType string) (StoredFile, error) {
	path, err := l.resolve(storageKey)
	if err != nil {
		return StoredFile{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return StoredFile{}, fmt.Errorf("storage: creating %s: %w", filepath.Dir(path), err)
	}
	// Attachments are statements and receipts.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return StoredFile{}, fmt.Errorf("storage: writing %s: %w", storageKey, err)
	}
	return StoredFile{StorageKey: storageKey, Size: int64(len(data)), ContentType: contentType}, nil
}

func (l *LocalStorage) Download(_ context.Context, storageKey string) ([]byte, error) {
	path, err := l.resolve(storageKey)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrFileNotFound, storageKey)
	}
	if err != nil {
		return nil, fmt.Errorf("storage: reading %s: %w", storageKey, err)
	}
	return data, nil
}

// Delete is idempotent: cleanup paths retry.
func (l *LocalStorage) Delete(_ context.Context, storageKey string) error {
	path, err := l.resolve(storageKey)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: deleting %s: %w", storageKey, err)
	}
	return nil
}

// URL is empty so files stream through the API's permission check.
func (l *LocalStorage) URL(string) string { return "" }
