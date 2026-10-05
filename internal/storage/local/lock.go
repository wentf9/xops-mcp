// Package local protects the deployment directory and its local transfer journal.
package local

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

func Lock(dir string, create bool, files ...string) (*flock.Flock, error) {
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect data directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("data directory must be a private directory (0700)")
	}
	for _, name := range append([]string{"server.lock"}, files...) {
		if info, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
				return nil, fmt.Errorf("%s must be a private regular file", name)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect deployment file: %w", err)
		}
	}
	lock := flock.New(filepath.Join(dir, "server.lock"), flock.SetPermissions(0600))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return nil, errors.Join(errors.New("deployment is already in use or cannot be locked"), err, lock.Close())
	}
	return lock, nil
}
