package fileutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWritePreservesPreviousFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("disk failure")
	err := Write(path, 0600, func(out io.Writer) error { _, _ = out.Write([]byte("partial")); return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old" {
		t.Fatalf("previous file lost: %q, %v", data, err)
	}
	if err := WriteFile(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("replacement failed: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v, %v", entries, err)
	}
}
func TestWriteDoesNotRemoveDestinationWhenReplaceFails(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "occupied")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(filepath.Join(destination, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(destination, []byte("new"), 0600); err == nil {
		t.Fatal("expected replace error")
	}
	if _, err := os.Stat(filepath.Join(destination, "keep")); err != nil {
		t.Fatal(err)
	}
}
