package api

import (
	"cmp"
	"errors"
	"io"
	"net/http"
)

// uploadedFile is one file part of a multipart body, read whole.
type uploadedFile struct {
	Bytes    []byte
	Filename string
}

// parseUpload reads a multipart body carrying at most limit bytes of files,
// plus a megabyte of form fields. ParseMultipartForm's argument is only what
// it keeps in memory, spilling the rest to temp files with no total bound;
// MaxBytesReader is the limit that refuses an oversized upload.
func parseUpload(w http.ResponseWriter, r *http.Request, part string, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return errInvalid("too_large", []string{"body", part},
				"That file is larger than %d MB", limit>>20)
		}
		return errInvalid("invalid", []string{"body"},
			"Send the file as a multipart form with a `%s` part", part)
	}
	return nil
}

// readUpload reads one file part of a body parseUpload read. found is false
// when the request carried none; a part that is empty or over limit is
// refused.
func readUpload(r *http.Request, part string, limit int64) (up uploadedFile, found bool, err error) {
	file, header, err := r.FormFile(part)
	if errors.Is(err, http.ErrMissingFile) {
		return uploadedFile{}, false, nil
	}
	if err != nil {
		return uploadedFile{}, false, errInvalid("invalid", []string{"body", part},
			"Send the file as a multipart form with a `%s` part", part)
	}
	defer func() { _ = file.Close() }()

	name := cmp.Or(header.Filename, "The file")
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return uploadedFile{}, false, err
	}
	if int64(len(raw)) > limit {
		return uploadedFile{}, false, errInvalid("too_large", []string{"body", part},
			"%s is larger than %d MB", name, limit>>20)
	}
	if len(raw) == 0 {
		return uploadedFile{}, false, errInvalid("invalid", []string{"body", part}, "%s is empty", name)
	}
	return uploadedFile{Bytes: raw, Filename: header.Filename}, true, nil
}

// requireUpload is readUpload for a part the request must carry.
func requireUpload(r *http.Request, part string, limit int64) (uploadedFile, error) {
	up, found, err := readUpload(r, part, limit)
	if err == nil && !found {
		err = errInvalid("missing", []string{"body", part}, "No file was attached")
	}
	return up, err
}

// removeMultipartTemp drops whatever ParseMultipartForm spilled to disk.
func removeMultipartTemp(r *http.Request) {
	if r.MultipartForm != nil {
		_ = r.MultipartForm.RemoveAll()
	}
}
