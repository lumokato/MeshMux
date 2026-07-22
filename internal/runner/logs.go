package runner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const maxLogLineBytes = 64 * 1024

type logPolicy struct {
	maxBytes int64
	backups  int
}

var (
	mihomoLogPolicy = logPolicy{maxBytes: 8 * 1024 * 1024, backups: 3}
	runnerLogPolicy = logPolicy{maxBytes: 2 * 1024 * 1024, backups: 2}
	runnerLogMu     sync.Mutex

	logURLPattern    = regexp.MustCompile(`(?i)(https?|socks5?|ss|vmess|vless|trojan|hysteria2?)://[^\s"'<>]+`)
	logSecretPattern = regexp.MustCompile(`(?i)(["']?(private[ _-]?key|pre[ _-]?shared[ _-]?key|auth[ _-]?key|api[ _-]?key|token|secret|password|authorization|provider[ _-]?url|subscription([ _-]?url)?)["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^,}\r\n]+)`)
)

func CleanupLogs() error {
	runnerLogMu.Lock()
	defer runnerLogMu.Unlock()
	var errs []error
	for _, item := range []struct {
		path   string
		policy logPolicy
	}{
		{filepath.Join("logs", "mihomo.out.log"), mihomoLogPolicy},
		{filepath.Join("logs", "mihomo.err.log"), mihomoLogPolicy},
		{filepath.Join("logs", "meshmux.log"), runnerLogPolicy},
	} {
		if err := cleanupLogSet(item.path, item.policy); err != nil {
			errs = append(errs, fmt.Errorf("cleanup %s: %w", item.path, err))
			continue
		}
		if err := sanitizeLogSet(item.path, item.policy); err != nil {
			errs = append(errs, fmt.Errorf("sanitize %s: %w", item.path, err))
		}
	}
	return errors.Join(errs...)
}

func sanitizeLogSet(path string, policy logPolicy) error {
	paths := []string{path}
	for index := 1; index <= policy.backups; index++ {
		paths = append(paths, fmt.Sprintf("%s.%d", path, index))
	}
	for _, candidate := range paths {
		data, err := os.ReadFile(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		sanitized := []byte(redactSensitiveText(string(data)))
		if bytes.Equal(data, sanitized) {
			continue
		}
		if err := replaceLogContents(candidate, sanitized); err != nil {
			return err
		}
	}
	return nil
}

func replaceLogContents(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpPath, path)
}

func newSanitizedRotatingLog(path string, policy logPolicy) (io.WriteCloser, error) {
	if err := cleanupLogSet(path, policy); err != nil {
		return nil, err
	}
	file, err := openRotatingFile(path, policy)
	if err != nil {
		return nil, err
	}
	return &sanitizingWriter{dst: file}, nil
}

func cleanupLogSet(path string, policy logPolicy) error {
	if policy.maxBytes <= 0 {
		return errors.New("log max size must be positive")
	}
	if policy.backups < 0 {
		return errors.New("log backup count must not be negative")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return fmt.Errorf("log path is a directory")
		}
		if info.Size() > policy.maxBytes {
			if err := os.Truncate(path, 0); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	matches, err := filepath.Glob(path + ".*")
	if err != nil {
		return err
	}
	for _, candidate := range matches {
		ext := strings.TrimPrefix(candidate, path+".")
		index, err := strconv.Atoi(ext)
		if err != nil || index < 1 {
			continue
		}
		info, statErr := os.Stat(candidate)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return statErr
		}
		if index > policy.backups || info.Size() > policy.maxBytes {
			if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

type rotatingFile struct {
	mu     sync.Mutex
	path   string
	policy logPolicy
	file   *os.File
	size   int64
}

func openRotatingFile(path string, policy logPolicy) (*rotatingFile, error) {
	r := &rotatingFile{path: path, policy: policy}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	r.file = file
	r.size = info.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if r.file == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.policy.maxBytes {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) rotate() error {
	if err := r.file.Close(); err != nil {
		return err
	}
	r.file = nil
	if r.policy.backups == 0 {
		if err := os.Truncate(r.path, 0); err != nil && !os.IsNotExist(err) {
			return err
		}
		return r.open()
	}

	oldest := fmt.Sprintf("%s.%d", r.path, r.policy.backups)
	if err := os.Remove(oldest); err != nil && !os.IsNotExist(err) {
		return err
	}
	for index := r.policy.backups - 1; index >= 1; index-- {
		source := fmt.Sprintf("%s.%d", r.path, index)
		target := fmt.Sprintf("%s.%d", r.path, index+1)
		if _, err := os.Stat(source); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := os.Rename(source, target); err != nil {
			return err
		}
	}
	if _, err := os.Stat(r.path); err == nil {
		if err := os.Rename(r.path, r.path+".1"); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return r.open()
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

type sanitizingWriter struct {
	mu         sync.Mutex
	dst        io.WriteCloser
	pending    []byte
	discarding bool
}

func (w *sanitizingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	originalLen := len(p)
	w.pending = append(w.pending, p...)
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			if w.discarding {
				w.pending = w.pending[:0]
			} else if len(w.pending) > maxLogLineBytes {
				if _, err := io.WriteString(w.dst, "[oversized log line omitted]\n"); err != nil {
					return originalLen, err
				}
				w.pending = w.pending[:0]
				w.discarding = true
			}
			break
		}
		line := w.pending[:newline]
		w.pending = w.pending[newline+1:]
		if w.discarding {
			w.discarding = false
			continue
		}
		if len(line) > maxLogLineBytes {
			if _, err := io.WriteString(w.dst, "[oversized log line omitted]\n"); err != nil {
				return originalLen, err
			}
			continue
		}
		if _, err := io.WriteString(w.dst, redactSensitiveText(strings.TrimSuffix(string(line), "\r"))+"\n"); err != nil {
			return originalLen, err
		}
	}
	return originalLen, nil
}

func (w *sanitizingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var writeErr error
	if len(w.pending) > 0 && !w.discarding {
		_, writeErr = io.WriteString(w.dst, redactSensitiveText(string(w.pending)))
	}
	w.pending = nil
	closeErr := w.dst.Close()
	return errors.Join(writeErr, closeErr)
}

func RedactLogText(text string) string {
	return redactSensitiveText(text)
}

func redactSensitiveText(text string) string {
	text = logSecretPattern.ReplaceAllString(text, "${1}[redacted]")
	return logURLPattern.ReplaceAllString(text, "[url-hidden]")
}

func readFileTail(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() == 0 || maxBytes <= 0 {
		return nil, nil
	}
	length := info.Size()
	if length > maxBytes {
		length = maxBytes
		if _, err := file.Seek(info.Size()-length, io.SeekStart); err != nil {
			return nil, err
		}
	}
	data := make([]byte, int(length))
	n, err := io.ReadFull(file, data)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return data[:n], nil
}
