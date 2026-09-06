package fileutil

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gofrs/flock"
)

var ErrBusy = errors.New("another MeshMux operation is in progress")

func TryLock(path string) (func(), error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(absolute)
	acquired, err := lock.TryLock()
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock operation: %w", err)
	}
	if !acquired {
		_ = lock.Close()
		return nil, ErrBusy
	}
	var once sync.Once
	return func() { once.Do(func() { _ = lock.Close() }) }, nil
}
