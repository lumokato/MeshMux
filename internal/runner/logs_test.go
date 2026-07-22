package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizedRotatingLogRedactsAcrossWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihomo.out.log")
	writer, err := newSanitizedRotatingLog(path, logPolicy{maxBytes: 1024, backups: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"level=error subscription=https://secret.",
		"example/path?token=abc\nPrivate",
		"Key = super-secret-key\n{\"token\":\"json-secret\"}\n",
	} {
		if _, err := writer.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"secret.example", "token=abc", "super-secret-key", "json-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("log contains %q:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatalf("redaction marker missing:\n%s", text)
	}
}

func TestRotatingLogEnforcesSizeAndBackupLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihomo.err.log")
	policy := logPolicy{maxBytes: 48, backups: 2}
	writer, err := newSanitizedRotatingLog(path, policy)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 12; index++ {
		if _, err := fmt.Fprintf(writer, "level=warning item=%02d\n", index); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= policy.backups; index++ {
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("stat %s: %v", candidate, err)
		}
		if info.Size() > policy.maxBytes {
			t.Fatalf("%s size = %d", candidate, info.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: %v", err)
	}
}

func TestCleanupLogSetTruncatesHugeActiveLogAndCapsBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihomo.out.log")
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 8_567_254_340); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".1", []byte("token=legacy-secret\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".2", make([]byte, 1025), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".3", []byte("expired"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cleanupLogSet(path, logPolicy{maxBytes: 1024, backups: 2}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("active log size = %d", info.Size())
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("valid backup removed: %v", err)
	}
	if err := sanitizeLogSet(path, logPolicy{maxBytes: 1024, backups: 2}); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(backup), "legacy-secret") {
		t.Fatalf("legacy backup was not sanitized: %q", backup)
	}
	for _, candidate := range []string{path + ".2", path + ".3"} {
		if _, err := os.Stat(candidate); !os.IsNotExist(err) {
			t.Fatalf("oversized or excess backup retained: %s (%v)", candidate, err)
		}
	}
}

func TestRecentLogTextReadsOnlyTailAndRedacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mihomo.err.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const size = int64(256 * 1024 * 1024)
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	marker := []byte("level=error token=tail-secret\n")
	if _, err := file.WriteAt(marker, size-int64(len(marker))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	text := recentLogText(path)
	if !strings.Contains(text, "level=error") || strings.Contains(text, "tail-secret") {
		t.Fatalf("unexpected log tail: %q", text)
	}
}
