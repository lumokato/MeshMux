package fileutil

import (
	"io"
	"os"
	"path/filepath"
)

func WriteFile(path string, data []byte, mode os.FileMode) error {
	return Write(path, mode, func(out io.Writer) error {
		_, err := out.Write(data)
		return err
	})
}

func Write(path string, mode os.FileMode, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	created := missingDirs(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".meshmux-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if err := write(temp); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := replace(temp.Name(), path); err != nil {
		return err
	}
	restoreOwner(path, created)
	return nil
}

// missingDirs lists the directories below dir that do not exist yet, outermost
// first, so a sudo run can hand them back to the invoking user.
func missingDirs(dir string) []string {
	var missing []string
	for p := filepath.Clean(dir); ; {
		if _, err := os.Stat(p); err == nil {
			break
		}
		missing = append(missing, p)
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	for i, j := 0, len(missing)-1; i < j; i, j = i+1, j-1 {
		missing[i], missing[j] = missing[j], missing[i]
	}
	return missing
}
