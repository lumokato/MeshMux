//go:build windows

package runner

import (
	"fmt"
	"golang.org/x/sys/windows/registry"
	"testing"
	"time"
)

func TestProxyValueBackupRestoresExactValuesInIsolatedKey(t *testing.T) {
	path := fmt.Sprintf(`Software\MeshMuxTests\Proxy-%d`, time.Now().UnixNano())
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.ALL_ACCESS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		key.Close()
		if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil {
			t.Error(err)
		}
	})
	if err := key.SetStringValue("ProxyServer", "other.example:8080"); err != nil {
		t.Fatal(err)
	}
	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		t.Fatal(err)
	}
	old, err := captureProxyValues(key)
	if err != nil {
		t.Fatal(err)
	}
	_ = key.SetStringValue("ProxyServer", "127.0.0.1:2080")
	_ = key.SetStringValue("ProxyOverride", "new")
	_ = key.SetDWordValue("ProxyEnable", 0)
	if err := restoreProxyValues(key, old); err != nil {
		t.Fatal(err)
	}
	server, _, _ := key.GetStringValue("ProxyServer")
	enabled, _, _ := key.GetIntegerValue("ProxyEnable")
	if server != "other.example:8080" || enabled != 1 {
		t.Fatal("previous settings not restored")
	}
	if _, _, err := key.GetValue("ProxyOverride", nil); err == nil {
		t.Fatal("new value survived rollback")
	}
}
