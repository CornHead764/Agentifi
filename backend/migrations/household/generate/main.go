// Command generate refreshes the generated change-capture migration section.
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/CornHead764/agentifi/backend/migrations/household"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	const path = "00001_initial.sql"
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	begin := []byte("-- BEGIN GENERATED CHANGE CAPTURE\n")
	end := []byte("-- END GENERATED CHANGE CAPTURE\n")
	start := bytes.Index(data, begin)
	stop := bytes.Index(data, end)
	if start < 0 || stop < start {
		return fmt.Errorf("%s: missing change-capture markers", path)
	}
	start += len(begin)
	updated := append([]byte(nil), data[:start]...)
	updated = append(updated, household.ChangeCaptureSQL()...)
	updated = append(updated, data[stop:]...)
	return os.WriteFile(path, updated, 0644)
}
