package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ftyp is an ISO base media file's leading box with this major brand.
func ftyp(brand string) []byte {
	box := append([]byte{0, 0, 0, 0x14, 'f', 't', 'y', 'p'}, brand...)
	return append(box, 0, 0, 0, 0, 'm', 'i', 'f', '1', 'p', 'a', 'd', 's')
}

func TestAPhoneCameraPhotoIsRecognisedByItsBoxHeader(t *testing.T) {
	cases := []struct {
		brand, contentType, extension string
	}{
		{"heic", "image/heic", ".heic"},
		{"heix", "image/heic", ".heic"},
		{"mif1", "image/heif", ".heif"},
	}
	for _, c := range cases {
		contentType, extension, ok := SniffDocument(ftyp(c.brand))
		require.True(t, ok, c.brand)
		require.Equal(t, c.contentType, contentType, c.brand)
		require.Equal(t, c.extension, extension, c.brand)
	}
}

func TestAVideoInTheSameContainerIsRefused(t *testing.T) {
	_, _, ok := SniffDocument(ftyp("qt  "))
	require.False(t, ok)
	_, _, ok = SniffDocument([]byte("ftyp"))
	require.False(t, ok)
}
