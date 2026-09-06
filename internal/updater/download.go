package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/meshmux/meshmux/internal/config"
	"github.com/meshmux/meshmux/internal/fileutil"
)

type release struct {
	Assets []asset `json:"assets"`
}

type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

var downloadMu sync.Mutex

func Download(component config.Component, kind string) (string, error) {
	if !downloadMu.TryLock() {
		return "", fmt.Errorf("another component download is in progress")
	}
	defer downloadMu.Unlock()
	unlock, err := fileutil.TryLock(filepath.Join("state", "core-operation.lock"))
	if err != nil {
		return "", err
	}
	defer unlock()
	if kind != "mihomo" && kind != "dashboard" {
		return "", fmt.Errorf("unsupported component kind %q", kind)
	}
	if component.Repo == "" {
		return "", fmt.Errorf("%s repo is empty", kind)
	}
	if component.AssetPattern == "" {
		return "", fmt.Errorf("%s asset pattern is empty", kind)
	}
	if component.Path == "" {
		return "", fmt.Errorf("%s path is empty", kind)
	}
	asset, err := releaseAsset(component.Repo, component.ReleaseTag, component.AssetPattern)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll("downloads", 0755); err != nil {
		return "", err
	}
	if !filepath.IsLocal(asset.Name) || strings.ContainsAny(asset.Name, "/\\:") {
		return "", fmt.Errorf("unsafe release asset name %q", asset.Name)
	}
	downloadDir, err := os.MkdirTemp("downloads", "component-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(downloadDir)
	archivePath := filepath.Join(downloadDir, asset.Name)
	expected, err := assetChecksum(component, asset)
	if err != nil {
		return "", err
	}
	if err := downloadFile(asset.URL, archivePath); err != nil {
		return "", err
	}
	if err := verifyChecksum(archivePath, expected); err != nil {
		return "", err
	}
	switch kind {
	case "mihomo":
		return installMihomo(archivePath, component.Path)
	case "dashboard":
		return installDashboard(archivePath, component.Path)
	default:
		return "", fmt.Errorf("unsupported component kind %q", kind)
	}
}

func releaseAsset(repo, tag, pattern string) (asset, error) {
	endpoint := "https://api.github.com/repos/" + repo + "/releases/latest"
	if tag != "" {
		endpoint = "https://api.github.com/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
	}
	resp, err := httpClient().Get(endpoint)
	if err != nil {
		return asset{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return asset{}, fmt.Errorf("GitHub release query failed: %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return asset{}, err
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return asset{}, err
	}
	for _, asset := range rel.Assets {
		if re.MatchString(asset.Name) {
			return asset, nil
		}
	}
	return asset{}, fmt.Errorf("no asset matching %q in %s", pattern, repo)
}

func downloadFile(rawURL, path string) error {
	resp, err := httpClient().Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	return fileutil.Write(path, 0600, func(out io.Writer) error {
		_, err := copyLimited(out, resp.Body, maxDownloadBytes)
		return err
	})
}

func installMihomo(archivePath, targetPath string) (string, error) {
	tmp, err := os.MkdirTemp(filepath.Dir(archivePath), "mihomo-extract-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := extractArchive(archivePath, tmp); err != nil {
		return "", err
	}
	var executable string
	err = filepath.WalkDir(tmp, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || executable != "" {
			return err
		}
		if isMihomoExecutableName(d.Name()) {
			executable = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if executable == "" {
		return "", fmt.Errorf("mihomo executable not found in archive")
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", err
	}
	if err := copyFile(executable, targetPath); err != nil {
		return "", err
	}
	if err := os.Chmod(targetPath, 0755); err != nil {
		return "", err
	}
	return targetPath, nil
}

func isMihomoExecutableName(name string) bool {
	name = strings.ToLower(filepath.Base(name))
	if name == "mihomo.exe" || name == "mihomo" {
		return true
	}
	if strings.HasPrefix(name, "mihomo-windows-") {
		return strings.HasSuffix(name, ".exe")
	}
	if !strings.HasPrefix(name, "mihomo-linux-") {
		return false
	}
	for _, suffix := range []string{".zip", ".gz", ".tgz", ".tar", ".zst", ".deb", ".rpm", ".yaml", ".yml", ".json", ".txt", ".md", ".patch"} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
}

func installDashboard(archivePath, targetPath string) (string, error) {
	target, err := filepath.Abs(targetPath)
	if err != nil {
		return "", err
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(target, workingDir)
	if err == nil && (relative == "." || filepath.IsLocal(relative)) {
		return "", fmt.Errorf("dashboard target must not contain the working directory")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return "", err
	}
	temp, err := os.MkdirTemp(filepath.Dir(target), ".dashboard-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if temp != "" {
			_ = os.RemoveAll(temp)
		}
	}()
	extracted := filepath.Join(temp, "extracted")
	if err := extractArchive(archivePath, extracted); err != nil {
		return "", err
	}
	root := findDashboardRoot(extracted)
	if root == "" {
		return "", fmt.Errorf("dashboard archive has no index.html")
	}
	backup := filepath.Join(temp, "previous")
	if err := os.Rename(target, backup); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(root, target); err != nil {
		if _, statErr := os.Stat(backup); statErr == nil {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				recovery := temp
				temp = ""
				return "", fmt.Errorf("install failed: %v; restore failed: %v; previous dashboard retained at %s", err, restoreErr, recovery)
			}
		}
		return "", err
	}
	return targetPath, nil
}

func extractArchive(path, dest string) error {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(path, dest)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(path, dest)
	case strings.HasSuffix(lower, ".gz"):
		return extractGzip(path, dest)
	default:
		return fmt.Errorf("unsupported archive type: %s", path)
	}
}

func extractZip(path, dest string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer reader.Close()
	if len(reader.File) > maxArchiveEntries {
		return fmt.Errorf("archive has too many entries")
	}
	remaining := maxExpandedBytes
	for _, file := range reader.File {
		if !filepath.IsLocal(file.Name) || strings.Contains(file.Name, ":") || file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe zip path: %s", file.Name)
		}
		name := filepath.Clean(file.Name)
		if name == "." {
			continue
		}
		target := filepath.Join(dest, name)
		cleanDest := filepath.Clean(dest)
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe zip path: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		mode := file.Mode().Perm()
		if mode == 0 {
			mode = 0644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			_ = in.Close()
			return err
		}
		count, copyErr := copyLimited(out, in, remaining)
		remaining -= count
		_ = in.Close()
		closeErr := out.Close()
		if copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			return copyErr
		}
		if err := os.Chmod(target, mode); err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(path, dest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	remaining := maxExpandedBytes
	entries := 0
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxArchiveEntries || header.Size < 0 || header.Size > remaining {
			return fmt.Errorf("archive exceeds extraction limits")
		}
		if !filepath.IsLocal(header.Name) || strings.Contains(header.Name, ":") {
			return fmt.Errorf("unsafe tar path: %s", header.Name)
		}
		if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
			return fmt.Errorf("unsupported tar entry: %s", header.Name)
		}
		name := filepath.Clean(header.Name)
		if name == "." {
			continue
		}
		target := filepath.Join(dest, name)
		cleanDest := filepath.Clean(dest)
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe tar path: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			mode := header.FileInfo().Mode().Perm()
			if mode == 0 {
				mode = 0644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			count, copyErr := copyLimited(out, tr, remaining)
			remaining -= count
			closeErr := out.Close()
			if copyErr == nil {
				copyErr = closeErr
			}
			if copyErr != nil {
				return copyErr
			}
			if err := os.Chmod(target, mode); err != nil {
				return err
			}
		}
	}
}

func extractGzip(path, dest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	name := filepath.Base(strings.TrimSpace(gz.Name))
	if name == "" || name == "." {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	target := filepath.Join(dest, name)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	_, copyErr := copyLimited(out, gz, maxExpandedBytes)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(target, 0755)
}

func findDashboardRoot(root string) string {
	var found string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || found != "" {
			return nil
		}
		if info, err := os.Stat(filepath.Join(path, "index.html")); err == nil && info.Mode().IsRegular() {
			found = path
		}
		return nil
	})
	return found
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	mode := info.Mode().Perm()
	if mode == 0 {
		mode = 0644
	}
	return fileutil.Write(dst, mode, func(out io.Writer) error {
		_, err := io.Copy(out, in)
		return err
	})
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}

const (
	maxDownloadBytes        int64 = 512 << 20
	maxExpandedBytes        int64 = 1 << 30
	maxArchiveEntries             = 20000
	pinnedWindowsCoreSHA256       = "0338285cfb7ec7c525d955387b14681b72d7b289730654ecd51a1f94bdad5019"
)

func assetChecksum(component config.Component, selected asset) (string, error) {
	expected := strings.ToLower(strings.TrimSpace(component.SHA256))
	if expected == "" && component.Repo == config.DefaultMihomoRepo && component.ReleaseTag == config.DefaultMihomoReleaseTag && selected.Name == "mihomo-windows-amd64-compatible-v1.19.29-meshmux.2.zip" {
		expected = pinnedWindowsCoreSHA256
	}
	if expected == "" {
		expected = strings.TrimPrefix(selected.Digest, "sha256:")
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("component requires a SHA-256 pin or a GitHub asset SHA-256 digest")
	}
	return strings.ToLower(expected), nil
}

func verifyChecksum(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), expected) {
		return fmt.Errorf("component SHA-256 mismatch; installation unchanged")
	}
	return nil
}

func copyLimited(out io.Writer, in io.Reader, limit int64) (int64, error) {
	count, err := io.Copy(out, io.LimitReader(in, limit+1))
	if err != nil {
		return count, err
	}
	if count > limit {
		return count, fmt.Errorf("component exceeds byte limit %d", limit)
	}
	return count, nil
}
