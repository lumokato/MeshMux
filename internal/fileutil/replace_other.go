//go:build !windows

package fileutil

import "os"

func replace(source, destination string) error {
	return os.Rename(source, destination)
}
