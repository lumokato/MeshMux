package publisher

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/meshmux/meshmux/internal/config"
)

func TestPublishSubStoreFileUpdatesDetectedExistingFile(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "mobile.yaml")
	if err := os.WriteFile(input, []byte("proxies: []\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var patched, posted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/files":
			_, _ = w.Write([]byte(`{"data":[{"name":"existing-mobile"}]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/file/existing-mobile":
			patched = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/files":
			posted = true
			http.Error(w, "should not create", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := Publish(config.PublishTarget{
		Name:   "mobile-substore",
		Type:   "substore-files",
		Input:  input,
		APIURL: server.URL + "/api/files",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !patched || posted {
		t.Fatalf("patched=%t posted=%t", patched, posted)
	}
	if result.FileName != "existing-mobile" || result.StatusCode != http.StatusOK {
		t.Fatalf("result = %+v", result)
	}
}

func TestSubStoreFileItemsSupportsCommonShapes(t *testing.T) {
	cases := []any{
		[]any{map[string]any{"name": "a"}},
		map[string]any{"data": []any{map[string]any{"name": "b"}}},
		map[string]any{"files": []any{map[string]any{"name": "c"}}},
		map[string]any{"name": "d"},
	}
	for _, tc := range cases {
		if got := subStoreFileItems(tc); len(got) != 1 {
			t.Fatalf("items for %#v = %#v", tc, got)
		}
	}
}
