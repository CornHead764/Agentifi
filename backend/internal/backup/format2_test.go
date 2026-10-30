package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The format-2 writer as the release before format 3 shipped it, so the
// tests can make the sets an upgraded install still has to restore.

// WriteFormat2 writes a format-2 set into dir.
func WriteFormat2(ctx context.Context, dir string, src Source, recipients []string, trigger Trigger, now time.Time) (Set, error) {
	sealTo, keys, err := parseRecipients(recipients)
	if err != nil {
		return Set{}, err
	}
	name := NewName(now, trigger)
	partial := filepath.Join(dir, partialPrefix+name)
	if err := os.Mkdir(partial, 0o700); err != nil {
		return Set{}, err
	}
	manifest := Manifest{
		Format: tarFormat, Name: name, CreatedAt: now, Trigger: trigger, Encrypted: len(sealTo) > 0,
		Recipients: keys, SchemaVersion: src.SchemaVersion, CredentialKeyID: src.CredentialKeyID,
		Files: map[Part]File{}, Verified: true,
	}
	suffix := ""
	if manifest.Encrypted {
		suffix = ".age"
	}
	database, err := writePart(partial, "database.tar.gz"+suffix, sealTo, func(w io.Writer) (File, error) {
		return snapshotDatabases(ctx, src.DataDir, w)
	})
	if err != nil {
		return Set{}, err
	}
	manifest.Files[PartDatabase] = database
	attachments, err := writePart(partial, "attachments.tar.gz"+suffix, sealTo, func(w io.Writer) (File, error) {
		return tarAttachments(filepath.Join(src.DataDir, "attachments"), w)
	})
	if err != nil {
		return Set{}, err
	}
	manifest.Files[PartAttachments] = attachments
	if err := writeManifest(partial, manifest); err != nil {
		return Set{}, err
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(partial, final); err != nil {
		return Set{}, err
	}
	set, _ := readSet(final)
	return set, nil
}

func snapshotDatabases(ctx context.Context, dir string, w io.Writer) (File, error) {
	ids, err := householdIDs(dir)
	if err != nil {
		return File{}, err
	}
	files := []string{"server.db"}
	for _, id := range ids {
		files = append(files, "households/"+id+".db")
	}
	scratch, err := os.MkdirTemp("", "agentifi-snapshots-")
	if err != nil {
		return File{}, err
	}
	defer os.RemoveAll(scratch)
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, rel := range files {
		target := filepath.Join(scratch, rel)
		if err := snapshotFile(ctx, filepath.Join(dir, rel), target); err != nil {
			return File{}, err
		}
		info, err := os.Stat(target)
		if err != nil {
			return File{}, err
		}
		if err := tw.WriteHeader(&tar.Header{Name: "databases/" + rel, Mode: 0o600, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			return File{}, err
		}
		if err := copyFile(tw, target); err != nil {
			return File{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return File{}, err
	}
	return File{Count: len(files)}, gz.Close()
}

func tarAttachments(root string, w io.Writer) (File, error) {
	var file File
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := walkAttachments(root, func(rel, path string, info fs.FileInfo) error {
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = "attachments/" + rel
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		file.Count++
		return copyFile(tw, path)
	})
	if err != nil {
		return File{}, fmt.Errorf("backup: archiving the attachments: %w", err)
	}
	if err := tw.Close(); err != nil {
		return File{}, err
	}
	return file, gz.Close()
}
