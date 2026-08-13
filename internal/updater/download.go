package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

type release struct {
	Assets []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func Download(component config.Component, kind string) (string, error) {
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
	archivePath := filepath.Join("downloads", asset.Name)
	if err := downloadFile(asset.URL, archivePath); err != nil {
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
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
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
	tmp := path + ".part"
	_ = os.Remove(tmp)
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}

func installMihomo(archivePath, targetPath string) (string, error) {
	tmp := filepath.Join("downloads", "mihomo-extract")
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0755); err != nil {
		return "", err
	}
	if err := extractArchive(archivePath, tmp); err != nil {
		return "", err
	}
	var executable string
	err := filepath.WalkDir(tmp, func(path string, d os.DirEntry, err error) error {
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
	tmp := filepath.Join("downloads", "dashboard-extract")
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0755); err != nil {
		return "", err
	}
	if err := extractArchive(archivePath, tmp); err != nil {
		return "", err
	}
	root := findDashboardRoot(tmp)
	if root == "" {
		root = tmp
	}
	_ = os.RemoveAll(targetPath)
	if err := copyDir(root, targetPath); err != nil {
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
	for _, file := range reader.File {
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
		_, copyErr := io.Copy(out, in)
		_ = in.Close()
		_ = out.Close()
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
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
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
			_, copyErr := io.Copy(out, tr)
			_ = out.Close()
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
	_, copyErr := io.Copy(out, gz)
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
		if _, err := os.Stat(filepath.Join(path, "index.html")); err == nil {
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
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(dst, mode)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}
