package publisher

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/meshmux/meshmux/internal/config"
)

type Result struct {
	URL        string
	StatusCode int
	SHA256     string
	FileName   string
}

func Publish(target config.PublishTarget) (Result, error) {
	switch target.Type {
	case "manual":
		data, err := os.ReadFile(target.Input)
		if err != nil {
			return Result{}, err
		}
		sum := sha256.Sum256(data)
		return Result{URL: target.RemoteURL, SHA256: hex.EncodeToString(sum[:])}, nil
	case "substore-files":
		return publishSubStoreFile(target)
	default:
		return Result{}, fmt.Errorf("unsupported publish target type %q", target.Type)
	}
}

func Probe(target config.PublishTarget) (Result, error) {
	apiPath := target.APIPath
	if apiPath == "" {
		apiPath = "/api/files"
	}
	endpoint, err := subStoreEndpoint(target, apiPath)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}, err
	}
	if target.TokenEnv != "" {
		if token := os.Getenv(target.TokenEnv); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Result{}, fmt.Errorf("substore files check failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return Result{URL: endpoint, StatusCode: resp.StatusCode}, nil
}

func publishSubStoreFile(target config.PublishTarget) (Result, error) {
	if target.Input == "" {
		return Result{}, fmt.Errorf("publish target %q has empty input", target.Name)
	}
	data, err := os.ReadFile(target.Input)
	if err != nil {
		return Result{}, err
	}
	fileName := target.FileName
	apiPath := target.APIPath
	if apiPath == "" {
		apiPath = "/api/files"
	}
	endpoint, err := subStoreEndpoint(target, apiPath)
	if err != nil {
		return Result{}, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if fileName == "" {
		if detected, err := detectSubStoreFileName(client, endpoint, target.TokenEnv); err == nil && detected != "" {
			fileName = detected
		}
	}
	if fileName == "" {
		fileName = "meshmux-mobile"
	}
	fileEndpoint, err := subStoreNamedFileURL(endpoint, fileName)
	if err != nil {
		return Result{}, err
	}

	payload := map[string]any{
		"name":         fileName,
		"displayName":  "",
		"display-name": "",
		"remark":       "",
		"icon":         "",
		"isIconColor":  true,
		"source":       "local",
		"sourceName":   "",
		"sourceType":   "collection",
		"tag":          []string{},
		"process":      []any{},
		"type":         "file",
		"content":      string(data),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}

	status, detail, err := sendSubStoreRequest(client, http.MethodPatch, fileEndpoint, body, target.TokenEnv)
	if err != nil {
		return Result{}, err
	}
	if status == http.StatusNotFound {
		status, detail, err = sendSubStoreRequest(client, http.MethodPost, endpoint, body, target.TokenEnv)
		if err != nil {
			return Result{}, err
		}
	}
	if status < 200 || status >= 300 {
		return Result{}, fmt.Errorf("substore files upload failed: HTTP %d%s", status, detail)
	}
	sum := sha256.Sum256(data)
	return Result{URL: fileEndpoint, StatusCode: status, SHA256: hex.EncodeToString(sum[:]), FileName: fileName}, nil
}

func sendSubStoreRequest(client *http.Client, method, endpoint string, body []byte, tokenEnv string) (int, string, error) {
	req, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if tokenEnv != "" {
		if token := os.Getenv(tokenEnv); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	var detail string
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if len(data) > 0 {
			detail = ": " + strings.TrimSpace(string(data))
		}
	}
	return resp.StatusCode, detail, nil
}

func subStoreEndpoint(target config.PublishTarget, path string) (string, error) {
	if strings.TrimSpace(target.APIURL) != "" {
		return target.APIURL, nil
	}
	if strings.TrimSpace(target.Backend) != "" {
		backend := strings.Trim(strings.TrimSpace(target.Backend), "/")
		return joinURL(target.BaseURL, backend+"/api/files")
	}
	return joinURL(target.BaseURL, path)
}

func detectSubStoreFileName(client *http.Client, endpoint, tokenEnv string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	if tokenEnv != "" {
		if token := os.Getenv(tokenEnv); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("list substore files failed: %s", resp.Status)
	}
	var raw any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return "", err
	}
	for _, item := range subStoreFileItems(raw) {
		if name, ok := item["name"].(string); ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name), nil
		}
	}
	return "", nil
}

func subStoreFileItems(raw any) []map[string]any {
	switch value := raw.(type) {
	case []any:
		return mapsFromList(value)
	case map[string]any:
		for _, key := range []string{"data", "files", "items", "list"} {
			if list, ok := value[key].([]any); ok {
				return mapsFromList(list)
			}
		}
		if _, ok := value["name"].(string); ok {
			return []map[string]any{value}
		}
	}
	return nil
}

func mapsFromList(list []any) []map[string]any {
	items := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items
}

func subStoreNamedFileURL(filesEndpoint, fileName string) (string, error) {
	u, err := url.Parse(filesEndpoint)
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(u.Path, "/")
	if strings.HasSuffix(base, "/files") {
		base = strings.TrimSuffix(base, "/files") + "/file"
	} else {
		base += "/file"
	}
	u.Path = strings.TrimRight(base, "/") + "/" + url.PathEscape(fileName)
	u.RawQuery = ""
	return u.String(), nil
}

func joinURL(base, path string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("base URL is empty")
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return u.String(), nil
}
