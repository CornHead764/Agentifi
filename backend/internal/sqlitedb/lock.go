package sqlitedb

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// Lock holds the server lock until Close. The lock file stays on disk.
type Lock struct {
	file *os.File
	once sync.Once
	err  error
}

func AcquireLock(path string) (*Lock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("sqlitedb: open instance lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("sqlitedb: another instance holds lock %q: %w", path, err)
		}
		return nil, fmt.Errorf("sqlitedb: acquire instance lock %q: %w", path, err)
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Close() error {
	l.once.Do(func() { l.err = l.file.Close() })
	return l.err
}
